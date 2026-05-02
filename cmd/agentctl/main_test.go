package main

import "testing"

func TestValidatePairingCode(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		wantErr bool
	}{
		{"valid all zeros", "000000", false},
		{"valid mixed digits", "428193", false},
		{"valid leading zero", "019283", false},
		{"empty", "", true},
		{"too short", "12345", true},
		{"too long", "1234567", true},
		{"contains letter", "12345a", true},
		{"contains symbol", "1234-5", true},
		{"contains internal space", "12 345", true},
		{"all letters", "abcdef", true},
		{"unicode digit (FULLWIDTH 1)", "１２３４５６", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePairingCode(tt.code)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validatePairingCode(%q) err = %v, wantErr = %v", tt.code, err, tt.wantErr)
			}
			if err != nil && err.Error() != errMsgInvalidCode {
				t.Errorf("err.Error() = %q, want %q (the spec's literal French string)", err.Error(), errMsgInvalidCode)
			}
		})
	}
}
