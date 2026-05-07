package workflow

import "testing"

func TestNewTemporalRuntimeRequiresStore(t *testing.T) {
	t.Parallel()

	if _, err := NewTemporalRuntime(nil, RuntimeConfig{}); err == nil {
		t.Fatal("expected nil store error")
	}
}

func TestTemporalRuntimeNilHelpers(t *testing.T) {
	t.Parallel()

	var runtime *TemporalRuntime
	if runtime.Client() != nil {
		t.Fatal("nil runtime should return nil client")
	}
	runtime.Close()

	empty := &TemporalRuntime{}
	empty.Close()
	if empty.Client() == nil {
		t.Fatal("non-nil runtime should return a client wrapper even when underlying client is nil")
	}
}
