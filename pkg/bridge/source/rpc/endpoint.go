package rpc

import "strings"

// EndpointProtocol represents the protocol type of an RPC endpoint
type EndpointProtocol string

const (
	// ProtocolWebSocket indicates ws:// or wss:// endpoint
	ProtocolWebSocket EndpointProtocol = "websocket"
	// ProtocolHTTP indicates http:// or https:// endpoint
	ProtocolHTTP EndpointProtocol = "http"
	// ProtocolUnknown indicates an unknown or invalid protocol
	ProtocolUnknown EndpointProtocol = "unknown"
)

// DetectEndpointProtocol determines the protocol type from an endpoint URL
func DetectEndpointProtocol(endpoint string) EndpointProtocol {
	lowered := strings.ToLower(endpoint)

	if strings.HasPrefix(lowered, "ws://") || strings.HasPrefix(lowered, "wss://") {
		return ProtocolWebSocket
	}

	if strings.HasPrefix(lowered, "http://") || strings.HasPrefix(lowered, "https://") {
		return ProtocolHTTP
	}

	return ProtocolUnknown
}

// IsWebSocketEndpoint checks if the endpoint uses WebSocket protocol
func IsWebSocketEndpoint(endpoint string) bool {
	return DetectEndpointProtocol(endpoint) == ProtocolWebSocket
}

// IsHTTPEndpoint checks if the endpoint uses HTTP protocol
func IsHTTPEndpoint(endpoint string) bool {
	return DetectEndpointProtocol(endpoint) == ProtocolHTTP
}
