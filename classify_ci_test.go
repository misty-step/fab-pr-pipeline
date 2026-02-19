package main

import (
	"testing"
)

func TestClassifyCIFailure(t *testing.T) {
	tests := []struct {
		name     string
		entries  []statusRollupEntry
		want     string
	}{
		{
			name:    "empty",
			entries: []statusRollupEntry{},
			want:    "unknown",
		},
		{
			name: "no failures",
			entries: []statusRollupEntry{
				{Typename: "CheckRun", Conclusion: "SUCCESS"},
				{Typename: "StatusContext", State: "SUCCESS"},
			},
			want: "unknown",
		},
		{
			name: "single lint",
			entries: []statusRollupEntry{
				{Typename: "CheckRun", Name: "golangci-lint", Conclusion: "FAILURE"},
			},
			want: "lint",
		},
		{
			name: "single test",
			entries: []statusRollupEntry{
				{Typename: "CheckRun", Name: "unit tests", Conclusion: "failure"},
			},
			want: "test",
		},
		{
			name: "single build",
			entries: []statusRollupEntry{
				{Typename: "CheckRun", Name: "build", Conclusion: "FAILURE"},
			},
			want: "build",
		},
		{
			name: "mixed",
			entries: []statusRollupEntry{
				{Typename: "CheckRun", Name: "lint check", Conclusion: "FAILURE"},
				{Typename: "CheckRun", Name: "pytest", Conclusion: "FAILURE"},
			},
			want: "mixed",
		},
		{
			name: "multiple same lint",
			entries: []statusRollupEntry{
				{Typename: "CheckRun", Name: "ESLint", Conclusion: "FAILURE"},
				{Typename: "CheckRun", Name: "prettier", Conclusion: "FAILURE"},
			},
			want: "lint",
		},
		{
			name: "unknown failure",
			entries: []statusRollupEntry{
				{Typename: "CheckRun", Name: "unknown-check", Conclusion: "FAILURE"},
			},
			want: "unknown",
		},
		{
			name: "case insensitive",
			entries: []statusRollupEntry{
				{Typename: "CheckRun", Name: "TypeCheck", Conclusion: "FaIlUrE"},
			},
			want: "build",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyCIFailure(tt.entries)
			if got != tt.want {
				t.Errorf("classifyCIFailure() = %q; want %q", got, tt.want)
			}
		})
	}
}