package analytics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Visualizer 提供极简的实时可视化：/ 可视化页面；/data 返回最新数据
type Visualizer struct {
	a *Analytics
	// 阈值历史，用于展示动态变化
	thAB []float64
	thBA []float64
}

func NewVisualizer(a *Analytics) *Visualizer {
	return &Visualizer{a: a}
}

// Start 在后台启动一个最简HTTP服务，实时展示 AB/BA 价差曲线和阈值线
func (v *Visualizer) Start(addr string) error {
	http.HandleFunc("/", v.handlePage)
	http.HandleFunc("/data", v.handleData)
	go func() { _ = http.ListenAndServe(addr, nil) }()
	return nil
}

// handleData 返回最近的数据点（最多300个）
func (v *Visualizer) handleData(w http.ResponseWriter, r *http.Request) {
	type resp struct {
		X           []int     `json:"x"`
		AB          []float64 `json:"ab"`
		BA          []float64 `json:"ba"`
		ThresholdAB []float64 `json:"thresholdAB"`
		ThresholdBA []float64 `json:"thresholdBA"`
		Time        int64     `json:"time"`
	}
	const maxN = 3000
	v.a.priceDiffsMu.RLock()
	ab := make([]float64, len(v.a.priceDiffs[0]))
	ba := make([]float64, len(v.a.priceDiffs[1]))
	copy(ab, v.a.priceDiffs[0])
	copy(ba, v.a.priceDiffs[1])
	v.a.priceDiffsMu.RUnlock()
	if len(ab) > maxN {
		ab = ab[len(ab)-maxN:]
	}
	if len(ba) > maxN {
		ba = ba[len(ba)-maxN:]
	}
	n := len(ab)
	if len(ba) < n {
		n = len(ba)
	}
	x := make([]int, n)
	abClip := make([]float64, n)
	baClip := make([]float64, n)
	for i := 0; i < n; i++ {
		x[i] = i
		abClip[i] = ab[len(ab)-n+i]
		baClip[i] = ba[len(ba)-n+i]
	}
	// 读取当前阈值并记录历史
	var curAB, curBA float64
	if v.a.optimalThresholds != nil {
		curAB = v.a.optimalThresholds.ThresholdAB
		curBA = v.a.optimalThresholds.ThresholdBA
	}
	v.thAB = append(v.thAB, curAB)
	v.thBA = append(v.thBA, curBA)
	if len(v.thAB) > maxN {
		v.thAB = v.thAB[len(v.thAB)-maxN:]
	}
	if len(v.thBA) > maxN {
		v.thBA = v.thBA[len(v.thBA)-maxN:]
	}
	// 对齐长度 n
	thABSeries := v.thAB
	thBASeries := v.thBA
	if len(thABSeries) > n {
		thABSeries = thABSeries[len(thABSeries)-n:]
	}
	if len(thBASeries) > n {
		thBASeries = thBASeries[len(thBASeries)-n:]
	}
	// 若阈值点少于 n，用最后一个值填充
	if len(thABSeries) < n {
		last := 0.0
		if len(thABSeries) > 0 {
			last = thABSeries[len(thABSeries)-1]
		}
		tmp := make([]float64, n)
		copy(tmp[n-len(thABSeries):], thABSeries)
		for i := 0; i < n-len(thABSeries); i++ {
			tmp[i] = last
		}
		thABSeries = tmp
	}
	if len(thBASeries) < n {
		last := 0.0
		if len(thBASeries) > 0 {
			last = thBASeries[len(thBASeries)-1]
		}
		tmp := make([]float64, n)
		copy(tmp[n-len(thBASeries):], thBASeries)
		for i := 0; i < n-len(thBASeries); i++ {
			tmp[i] = last
		}
		thBASeries = tmp
	}
	_ = json.NewEncoder(w).Encode(resp{
		X:           x,
		AB:          abClip,
		BA:          baClip,
		ThresholdAB: thABSeries,
		ThresholdBA: thBASeries,
		Time:        time.Now().Unix(),
	})
}

// handlePage 返回一个内嵌的最简可视化页面（Chart.js）
func (v *Visualizer) handlePage(w http.ResponseWriter, r *http.Request) {
	page := `<!doctype html>
<html>
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <title>Diff Visualizer</title>
  <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
  <style>
    body { margin: 0; background:#111; color:#eee; font:14px/1.4 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Helvetica,Arial; }
    .wrap { padding: 12px; display:flex; flex-direction:column; align-items:center; }
    .chart-box { width: 100%; max-width: 100%; }
    canvas { width:100%; height:220px; background:#181818; border:1px solid #222; }
  </style>
  </head>
<body>
  <div class="wrap">
    <h3>AB/BA Diff & Threshold</h3>
    <div class="chart-box">
      <canvas id="c"></canvas>
    </div>
  </div>
  <script>
    const ctx = document.getElementById('c').getContext('2d');
    const chart = new Chart(ctx, {
      type: 'line',
      data: {
        labels: [],
        datasets: [
          { label: '+A-B', data: [], borderColor: '#4ade80', backgroundColor: 'rgba(74,222,128,0.2)', tension: 0.2, pointRadius: 0, borderWidth: 2 },
          { label: '-A+B', data: [], borderColor: '#60a5fa', backgroundColor: 'rgba(96,165,250,0.2)', tension: 0.2, pointRadius: 0, borderWidth: 2 },
          { label: 'Threshold AB', data: [], borderColor: '#f59e0b', backgroundColor: 'rgba(245,158,11,0.15)', tension: 0, pointRadius: 0, borderWidth: 1, borderDash: [6,4] },
          { label: 'Threshold BA', data: [], borderColor: '#f43f5e', backgroundColor: 'rgba(244,63,94,0.12)', tension: 0, pointRadius: 0, borderWidth: 1, borderDash: [6,4] }
        ]
      },
      options: {
        responsive: true,
        animation: false,
        scales: {
          x: { ticks: { color: '#bbb' }, grid: { color: '#222' } },
          y: { ticks: { color: '#bbb' }, grid: { color: '#222' } }
        },
        plugins: {
          legend: { labels: { color: '#ddd' } }
        }
      }
    });
    async function tick() {
      try {
        const res = await fetch('/data');
        const j = await res.json();
        chart.data.labels = j.x;
        chart.data.datasets[0].data = j.ab;
        chart.data.datasets[1].data = j.ba;
         chart.data.datasets[2].data = j.thresholdAB;
         chart.data.datasets[3].data = j.thresholdBA;
        chart.update();
      } catch (_) {}
    }
    setInterval(tick, 500);
    tick();
  </script>
</body>
</html>`
	fmt.Fprint(w, page)
}
