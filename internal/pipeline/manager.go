package pipeline

import (
	"fmt"
	"sync"
)

// PipelineManager 管理所有 Pipeline 实例（内存管理）
type PipelineManager struct {
	mu        sync.RWMutex
	pipelines map[string]*Pipeline
}

var globalPipelineManager *PipelineManager
var once sync.Once

// GetPipelineManager 获取全局 PipelineManager 实例（单例模式）
func GetPipelineManager() *PipelineManager {
	once.Do(func() {
		globalPipelineManager = &PipelineManager{
			pipelines: make(map[string]*Pipeline),
		}
	})
	return globalPipelineManager
}

// CreatePipeline 创建并存储一个新的 Pipeline
func (pm *PipelineManager) CreatePipeline(name string, nodes []Node, edges []*EdgeConfig) (*Pipeline, error) {
	if name == "" {
		return nil, fmt.Errorf("pipeline name is required")
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("pipeline must have at least one node")
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	// 检查是否已存在同名 pipeline
	if _, exists := pm.pipelines[name]; exists {
		return nil, fmt.Errorf("pipeline with name %s already exists", name)
	}

	// 验证 ExchangeNode 充提能力（拦截 DEX 空壳节点）
	for _, node := range nodes {
		if exNode, ok := node.(*ExchangeNode); ok {
			if err := exNode.ValidateDepositWithdrawCapability(); err != nil {
				return nil, fmt.Errorf("node %s capability check failed: %w", node.GetID(), err)
			}
		}
	}

	// 创建 Pipeline
	pipeline := NewPipeline(name, nodes...)

	// 配置边
	for _, edge := range edges {
		if edge == nil {
			continue
		}
		if err := pipeline.SetEdgeConfig(edge.FromNodeID, edge.ToNodeID, edge); err != nil {
			return nil, fmt.Errorf("failed to set edge config: %w", err)
		}
	}

	// 存储到管理器
	pm.pipelines[name] = pipeline

	return pipeline, nil
}

// GetPipeline 根据 ID 获取 Pipeline
func (pm *PipelineManager) GetPipeline(id string) (*Pipeline, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	pipeline, ok := pm.pipelines[id]
	if !ok {
		return nil, fmt.Errorf("pipeline %s not found", id)
	}

	return pipeline, nil
}

// ListPipelines 列出所有 Pipeline
func (pm *PipelineManager) ListPipelines() []*Pipeline {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make([]*Pipeline, 0, len(pm.pipelines))
	for _, pipeline := range pm.pipelines {
		result = append(result, pipeline)
	}

	return result
}

// DeletePipeline 删除指定的 Pipeline
func (pm *PipelineManager) DeletePipeline(id string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, exists := pm.pipelines[id]; !exists {
		return fmt.Errorf("pipeline %s not found", id)
	}

	// 停止 pipeline（如果正在运行）
	pipeline := pm.pipelines[id]
	if pipeline.Status() == PipelineStatusRunning {
		pipeline.Stop()
	}

	delete(pm.pipelines, id)
	return nil
}

// GetPipelineStatus 获取 Pipeline 状态
func (pm *PipelineManager) GetPipelineStatus(id string) (PipelineStatus, error) {
	pipeline, err := pm.GetPipeline(id)
	if err != nil {
		return "", err
	}

	return pipeline.Status(), nil
}

// UpsertPipeline 存储（或覆盖）一个 Pipeline
// 用于测试/临时创建的 pipeline 注入到内存管理器中。
func (pm *PipelineManager) UpsertPipeline(p *Pipeline) error {
	if p == nil {
		return fmt.Errorf("pipeline is nil")
	}
	id := p.ID()
	if id == "" {
		return fmt.Errorf("pipeline id is empty")
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.pipelines[id] = p
	return nil
}
