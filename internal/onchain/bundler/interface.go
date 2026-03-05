package bundler

// Bundler 接口，用于通过 bundler 发送交易以降低 gas 费用
type Bundler interface {
	SendBundle(signedTx string, chainID string) (string, error)
	GetBundleStatus(bundleHash string, chainID string) (string, error)
	SupportsChain(chainID string) bool
	GetName() string
}

