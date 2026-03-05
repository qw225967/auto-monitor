package hyperliquid

import (
	"crypto/ecdsa"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

// VerifyWalletAddress 验证私钥是否与钱包地址匹配
func VerifyWalletAddress(privateKeyHex string, expectedAddress string) (derivedAddress string, match bool, err error) {
	// 去掉 0x 前缀
	privateKeyHex = strings.TrimPrefix(privateKeyHex, "0x")
	privateKeyHex = strings.TrimPrefix(privateKeyHex, "0X")
	
	// 解析私钥
	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return "", false, fmt.Errorf("failed to parse private key: %w", err)
	}
	
	// 获取公钥并派生地址
	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return "", false, fmt.Errorf("error casting public key to ECDSA")
	}
	
	address := crypto.PubkeyToAddress(*publicKeyECDSA)
	derivedAddress = address.Hex()
	
	// 比较地址（忽略大小写）
	expectedAddress = strings.TrimPrefix(expectedAddress, "0x")
	expectedAddress = strings.TrimPrefix(expectedAddress, "0X")
	derivedAddressClean := strings.TrimPrefix(derivedAddress, "0x")
	
	match = strings.EqualFold(expectedAddress, derivedAddressClean)
	
	return derivedAddress, match, nil
}
