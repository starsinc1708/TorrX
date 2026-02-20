package usecase

import (
	"errors"
	"testing"
)

func TestWrapEngine(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantNil bool
		wantIs  error
	}{
		{"nil error returns nil", nil, true, nil},
		{"wraps with ErrEngine", errors.New("boom"), false, ErrEngine},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapEngine(tt.err)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(got, tt.wantIs) {
				t.Fatalf("expected errors.Is(%v, %v) to be true", got, tt.wantIs)
			}
			if got.Error() == tt.err.Error() {
				t.Fatalf("wrapped error should differ from original")
			}
		})
	}
}

func TestWrapRepo(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantNil bool
		wantIs  error
	}{
		{"nil error returns nil", nil, true, nil},
		{"wraps with ErrRepository", errors.New("db down"), false, ErrRepository},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapRepo(tt.err)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(got, tt.wantIs) {
				t.Fatalf("expected errors.Is(%v, %v) to be true", got, tt.wantIs)
			}
			if got.Error() == tt.err.Error() {
				t.Fatalf("wrapped error should differ from original")
			}
		})
	}
}

func TestErrorSentinels(t *testing.T) {
	// Verify sentinel errors are distinct and have meaningful messages.
	sentinels := []struct {
		name string
		err  error
	}{
		{"ErrEngine", ErrEngine},
		{"ErrRepository", ErrRepository},
		{"ErrInvalidFileIndex", ErrInvalidFileIndex},
	}

	for _, s := range sentinels {
		t.Run(s.name, func(t *testing.T) {
			if s.err == nil {
				t.Fatalf("%s should not be nil", s.name)
			}
			if s.err.Error() == "" {
				t.Fatalf("%s should have a message", s.name)
			}
		})
	}

	// Verify they are distinct.
	if errors.Is(ErrEngine, ErrRepository) {
		t.Fatalf("ErrEngine and ErrRepository should be distinct")
	}
	if errors.Is(ErrEngine, ErrInvalidFileIndex) {
		t.Fatalf("ErrEngine and ErrInvalidFileIndex should be distinct")
	}
}
