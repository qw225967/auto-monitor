package bundler

// cleanHexPrefix 清理 hex 字符串的前缀
func cleanHexPrefix(s string) string {
	if len(s) >= 2 && s[0:2] == "0x" {
		return s[2:]
	}
	return s
}

