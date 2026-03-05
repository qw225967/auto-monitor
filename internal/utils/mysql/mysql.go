package mysql

import (
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
)

var DB *sql.DB

func InitDB(dsn string) error {
	var err error
	DB, err = sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	return DB.Ping()
}

func InsertSpreadStat(
	symbol string,
	ts int64,
	exPair string,
	marketType string,
	chainId string,
	exBuy, exSell, chainBuy, chainSell, minusAB, plusAB float64,
) error {
	stmt := `INSERT INTO spread_stat
		(symbol, ts, ex_pair, market_type, chainId, ex_buy, ex_sell, chain_buy, chain_sell, minus_ab_percent, plus_ab_percent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := DB.Exec(stmt, symbol, ts, exPair, marketType, chainId, exBuy, exSell, chainBuy, chainSell, minusAB, plusAB)
	if err != nil {
		fmt.Println(err)
		return fmt.Errorf("InsertSpreadStat error: %w", err)
	}
	return nil
}

// SpreadStatData 批量插入数据结构
type SpreadStatData struct {
	Symbol     string
	Ts         int64
	ExPair     string
	MarketType string
	ChainId    string
	ExBuy      float64
	ExSell     float64
	ChainBuy   float64
	ChainSell  float64
	MinusAB    float64
	PlusAB     float64
}

// BatchInsertSpreadStat 批量插入价差统计数据
func BatchInsertSpreadStat(data []SpreadStatData) error {
	if len(data) == 0 {
		return nil
	}

	// 准备批量插入语句
	stmt := `INSERT INTO spread_stat
		(symbol, ts, ex_pair, market_type, chainId, ex_buy, ex_sell, chain_buy, chain_sell, minus_ab_percent, plus_ab_percent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	// 开始事务
	tx, err := DB.Begin()
	if err != nil {
		return fmt.Errorf("BatchInsertSpreadStat begin transaction error: %w", err)
	}
	defer tx.Rollback()

	// 准备语句
	preparedStmt, err := tx.Prepare(stmt)
	if err != nil {
		return fmt.Errorf("BatchInsertSpreadStat prepare statement error: %w", err)
	}
	defer preparedStmt.Close()

	// 批量执行
	for _, item := range data {
		_, err := preparedStmt.Exec(
			item.Symbol, item.Ts, item.ExPair, item.MarketType, item.ChainId,
			item.ExBuy, item.ExSell, item.ChainBuy, item.ChainSell,
			item.MinusAB, item.PlusAB,
		)
		if err != nil {
			return fmt.Errorf("BatchInsertSpreadStat exec error: %w", err)
		}
	}

	// 提交事务
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("BatchInsertSpreadStat commit error: %w", err)
	}

	fmt.Printf("[BatchInsert] 成功批量插入 %d 条价差统计数据\n", len(data))
	return nil
}

