package queue

import (
	"errors"
	"testing"
)

type retryResolverStub struct {
	value int
	err   error
}

func (s retryResolverStub) GetMaxRetries(string) (int, error) {
	return s.value, s.err
}

func TestResolveMaxRetries(t *testing.T) {
	tests := []struct {
		name     string
		resolver MaxRetriesResolver
		current  int
		want     int
		wantErr  error
	}{
		{name: "negative means zero", current: -1, want: 0},
		{name: "explicit value is preserved", current: 7, want: 7},
		{name: "registry fallback", resolver: retryResolverStub{value: 4}, want: 4},
		{name: "nil resolver", wantErr: errRetryResolverNil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveMaxRetries(tt.resolver, "job.type", tt.current)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("value = %d, want %d", got, tt.want)
			}
		})
	}
}
