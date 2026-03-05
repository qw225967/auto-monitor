package json

import "encoding/json"

// MarshalToString json 序列化字符串
func MarshalToString(src interface{}) string {
	j, err := json.Marshal(src)
	if err != nil {
		return ""
	}
	return string(j)
}

// DeepCopy 通过 json 序列化和反序列化实现深拷贝
func DeepCopy(dst, src interface{}) error {
	bs, _ := json.Marshal(src)
	return json.Unmarshal(bs, dst)
}
