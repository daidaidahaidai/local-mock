package domain

// Bundle is a self-contained, validated mock artifact. Descriptors are a protobuf
// FileDescriptorSet; runtime adapters interpret it without accessing source files.
type Bundle struct {
	Name        string `json:"name"`
	Service     string `json:"service"`
	Descriptors []byte `json:"descriptors"`
	Rules       []Rule `json:"rules"`
}
type Rule struct {
	Method       string `json:"method"`
	Response     []byte `json:"response,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	DelayMS      int    `json:"delay_ms,omitempty"`
}
