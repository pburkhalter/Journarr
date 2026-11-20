package config

import (
	"strings"
	"testing"
	"time"
)

// The flow controller's delays (60s notify grouping, 45s scan coalescing) only
// hold if it ticks well below them. It used to borrow the 5m stuck interval,
// which delayed every WhatsApp notice by exactly five minutes.
func TestFlowTickDefaultIsBelowNotifyDelay(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.FlowTickInterval > 30*time.Second {
		t.Fatalf("FlowTickInterval = %s, want <= 30s", c.FlowTickInterval)
	}
	if c.FlowTickInterval == c.StuckPollInterval {
		t.Fatal("flow tick must not share the stuck poll interval")
	}
}

func TestLoadMCP(t *testing.T) {
	tok := func(c byte) string { return strings.Repeat(string(c), 32) }
	cases := []struct {
		name    string
		env     []string
		write   []string
		remove  string
		wantErr string
		want    map[string]string
	}{
		{name: "none", remove: "off", want: map[string]string{}},
		{name: "two clients", remove: "off", write: []string{"MAC"},
			env:  []string{"JOURNARR_MCP_TOKEN_MAC=" + tok('a'), "JOURNARR_MCP_TOKEN_HA=" + tok('b'), "OTHER=x"},
			want: map[string]string{"mac": tok('a'), "ha": tok('b')}},
		{name: "short token", remove: "off", env: []string{"JOURNARR_MCP_TOKEN_MAC=short"}, wantErr: ">= 32"},
		{name: "shared token", remove: "off", wantErr: "share a token",
			env: []string{"JOURNARR_MCP_TOKEN_MAC=" + tok('a'), "JOURNARR_MCP_TOKEN_HA=" + tok('a')}},
		{name: "writer without token", remove: "off", write: []string{"agentbox"},
			env: []string{"JOURNARR_MCP_TOKEN_MAC=" + tok('a')}, wantErr: "JOURNARR_MCP_WRITE names"},
		{name: "bad remove mode", remove: "yes", wantErr: "off or approval"},
		{name: "no suffix", remove: "off", env: []string{"JOURNARR_MCP_TOKEN_=" + tok('a')}, wantErr: "client name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{MCPWrite: tc.write, MCPRemove: tc.remove, MCPWriteLimit: 30}
			err := c.loadMCP(tc.env)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(c.MCPTokens) != len(tc.want) {
				t.Fatalf("tokens = %v, want %v", c.MCPTokens, tc.want)
			}
			for k, v := range tc.want {
				if c.MCPTokens[k] != v {
					t.Fatalf("token %s = %q", k, c.MCPTokens[k])
				}
			}
		})
	}
}
