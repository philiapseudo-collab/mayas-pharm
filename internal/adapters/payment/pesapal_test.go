package payment

import "testing"

func TestPesapalBaseURL(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{name: "sandbox default", env: "", want: pesapalSandboxBaseURL},
		{name: "sandbox explicit", env: "sandbox", want: pesapalSandboxBaseURL},
		{name: "production", env: "production", want: pesapalProductionBaseURL},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pesapalBaseURL(tc.env)
			if got != tc.want {
				t.Fatalf("pesapalBaseURL(%q) = %q, want %q", tc.env, got, tc.want)
			}
		})
	}
}

func TestNumericParsingHelpers(t *testing.T) {
	if got, err := intFromAny("3"); err != nil || got != 3 {
		t.Fatalf("intFromAny(string) = (%d, %v), want (3, nil)", got, err)
	}

	if got, err := floatFromAny("12.5"); err != nil || got != 12.5 {
		t.Fatalf("floatFromAny(string) = (%f, %v), want (12.5, nil)", got, err)
	}
}
