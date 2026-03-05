package hyperliquid

import (
	"crypto/ecdsa"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/vmihailenco/msgpack/v5"
)

// signOrderWithEIP712 使用 EIP-712 签名订单
func signOrderWithEIP712(privateKeyHex string, action interface{}, nonce int64) (map[string]interface{}, error) {
	// 去掉 0x 前缀
	privateKeyHex = strings.TrimPrefix(privateKeyHex, "0x")
	privateKeyHex = strings.TrimPrefix(privateKeyHex, "0X")
	
	// 解析私钥
	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}
	
	// 验证私钥格式
	publicKey := privateKey.Public()
	_, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("error casting public key to ECDSA")
	}
	
	// 使用 Msgpack 序列化 action（Hyperliquid 要求）
	actionBytes, err := msgpack.Marshal(action)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal action with msgpack: %w", err)
	}
	
	// 构建 action hash（关键：不仅仅是 msgpack(action)，还要追加 nonce 和 vaultAddress！）
	// 参考 go-hyperliquid SDK 的 actionHash 函数
	data := actionBytes
	
	// 追加 nonce (8 bytes big endian)
	nonceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBytes, uint64(nonce))
	data = append(data, nonceBytes...)
	
	// 追加 vaultAddress (0x00 表示无 vault)
	data = append(data, 0x00)
	
	// 现在对完整数据进行 keccak256 哈希
	actionHash := crypto.Keccak256(data)
	
	// 构建 EIP-712 类型化数据
	typedData := buildHyperliquidTypedData(actionHash, nonce)
	
	// 计算 EIP-712 哈希
	domainSeparator, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return nil, fmt.Errorf("failed to hash domain: %w", err)
	}
	
	typedDataHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return nil, fmt.Errorf("failed to hash message: %w", err)
	}
	
	// 构建完整的签名消息
	rawData := []byte(fmt.Sprintf("\x19\x01%s%s", string(domainSeparator), string(typedDataHash)))
	signHash := crypto.Keccak256Hash(rawData)
	
	// 签名
	signature, err := crypto.Sign(signHash.Bytes(), privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign: %w", err)
	}
	
	// 调整 v 值 (以太坊签名需要 27 或 28)
	if signature[64] < 27 {
		signature[64] += 27
	}
	
	// 构建签名对象
	r := hexutil.Encode(signature[:32])
	s := hexutil.Encode(signature[32:64])
	v := int(signature[64])
	
	return map[string]interface{}{
		"r": r,
		"s": s,
		"v": v,
	}, nil
}

// buildHyperliquidTypedData 构建 Hyperliquid 的 EIP-712 类型化数据
// 完全匹配 go-hyperliquid SDK 的实现
func buildHyperliquidTypedData(actionHash []byte, nonce int64) apitypes.TypedData {
	// 构建 phantom agent
	phantomAgent := map[string]interface{}{
		"source":       "a", // mainnet
		"connectionId": actionHash,
	}
	
	// Chain ID 固定为 1337 (from go-hyperliquid SDK)
	return apitypes.TypedData{
		Types: apitypes.Types{
			"Agent": []apitypes.Type{
				{Name: "source", Type: "string"},
				{Name: "connectionId", Type: "bytes32"},
			},
			"EIP712Domain": []apitypes.Type{
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
		},
		PrimaryType: "Agent",
		Domain: apitypes.TypedDataDomain{
			Name:              "Exchange",
			Version:           "1",
			ChainId:           math.NewHexOrDecimal256(1337),
			VerifyingContract: "0x0000000000000000000000000000000000000000",
		},
		Message: phantomAgent,
	}
}
