package onchain

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// convertToDecimals 转换金额到最小单位
func (o *okdex) convertToDecimals(amount, decimalsStr string) (string, error) {
	decimals, err := strconv.Atoi(decimalsStr)
	if err != nil {
		return "", fmt.Errorf("invalid decimals: %v", err)
	}

	if decimals < 0 || decimals > 77 {
		return "", fmt.Errorf("decimals out of range: %d", decimals)
	}

	if amount == "" {
		return "", fmt.Errorf("amount cannot be empty")
	}

	if len(amount) > 0 && amount[0] == '-' {
		return "", errors.New("amount cannot be negative")
	}

	dotIndex := strings.Index(amount, ".")
	var integerPart, fractionalPart string

	if dotIndex == -1 {
		integerPart = amount
		fractionalPart = ""
	} else {
		integerPart = amount[:dotIndex]
		fractionalPart = amount[dotIndex+1:]
	}

	integerPart = strings.TrimLeft(integerPart, "0")
	if integerPart == "" {
		integerPart = "0"
	}

	if len(fractionalPart) < decimals {
		fractionalPart = fractionalPart + strings.Repeat("0", decimals-len(fractionalPart))
	} else if len(fractionalPart) > decimals {
		fractionalPart = fractionalPart[:decimals]
	}

	resultStr := integerPart + fractionalPart
	resultInt := new(big.Int)
	if _, ok := resultInt.SetString(resultStr, 10); !ok {
		return "", fmt.Errorf("invalid amount format after conversion: %s", resultStr)
	}

	return resultInt.String(), nil
}

// convertFromDecimals 将最小单位金额转换为带精度的浮点表示
func (o *okdex) convertFromDecimals(amountStr, decimalsStr string) (*big.Float, error) {
	if amountStr == "" {
		return nil, fmt.Errorf("amount cannot be empty")
	}

	amountInt := new(big.Int)
	if _, ok := amountInt.SetString(amountStr, 10); !ok {
		return nil, fmt.Errorf("invalid amount: %s", amountStr)
	}

	decimals := 0
	if decimalsStr != "" {
		var err error
		decimals, err = strconv.Atoi(decimalsStr)
		if err != nil {
			return nil, fmt.Errorf("invalid decimals: %v", err)
		}
	}

	denominator := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	if denominator.Cmp(big.NewInt(0)) == 0 {
		return nil, fmt.Errorf("decimals denominator cannot be zero")
	}

	amountFloat := new(big.Float).SetPrec(256).SetInt(amountInt)
	denominatorFloat := new(big.Float).SetPrec(256).SetInt(denominator)
	result := new(big.Float).SetPrec(256)
	result.Quo(amountFloat, denominatorFloat)
	return result, nil
}

// normalizeTokenAddress 规范化 token 地址，处理 API 可能返回数字的情况
func (o *okdex) normalizeTokenAddress(addr string) string {
	if addr == "" {
		return ""
	}

	addr = strings.TrimSpace(addr)
	addr = strings.ToLower(addr)
	if strings.HasPrefix(addr, "0x") {
		addr = addr[2:]
	}

	if isNumeric(addr) {
		bigInt := new(big.Int)
		if _, ok := bigInt.SetString(addr, 10); !ok {
			return addr
		}
		hexStr := fmt.Sprintf("%x", bigInt)
		if len(hexStr) < 40 {
			hexStr = strings.Repeat("0", 40-len(hexStr)) + hexStr
		} else if len(hexStr) > 40 {
			hexStr = hexStr[len(hexStr)-40:]
		}
		return hexStr
	}

	if len(addr) < 40 {
		addr = strings.Repeat("0", 40-len(addr)) + addr
	} else if len(addr) > 40 {
		addr = addr[len(addr)-40:]
	}

	return addr
}

// isNumeric 检查字符串是否是纯数字
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

