package service

import (
	"testing"

	"github.com/philia-technologies/mayas-pharm/internal/core"
)

func TestClassifyPesapalStatus(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		description string
		want        core.OrderStatus
	}{
		{
			name:        "code 1 is paid",
			statusCode:  1,
			description: "Completed",
			want:        core.OrderStatusPaid,
		},
		{
			name:        "code 2 is failed",
			statusCode:  2,
			description: "Failed",
			want:        core.OrderStatusFailed,
		},
		{
			name:        "text cancellation is failed",
			statusCode:  0,
			description: "CANCELLED",
			want:        core.OrderStatusFailed,
		},
		{
			name:        "unknown remains pending",
			statusCode:  0,
			description: "PENDING",
			want:        core.OrderStatusPending,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyPesapalStatus(tc.statusCode, tc.description)
			if got != tc.want {
				t.Fatalf("classifyPesapalStatus(%d, %q) = %s, want %s", tc.statusCode, tc.description, got, tc.want)
			}
		})
	}
}

func TestNormalizePesapalPaymentMethod(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output string
	}{
		{
			name:   "airtel money stays explicit",
			input:  "Airtel Money",
			output: "AIRTEL_MONEY",
		},
		{
			name:   "visa maps to card",
			input:  "VISA",
			output: string(core.PaymentMethodCard),
		},
		{
			name:   "mpesa maps to mpesa",
			input:  "M-Pesa",
			output: string(core.PaymentMethodMpesa),
		},
		{
			name:   "unknown is normalized and preserved",
			input:  "Mobile Wallet",
			output: "MOBILE_WALLET",
		},
		{
			name:   "empty stays empty",
			input:  "",
			output: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizePesapalPaymentMethod(tc.input)
			if got != tc.output {
				t.Fatalf("normalizePesapalPaymentMethod(%q) = %q, want %q", tc.input, got, tc.output)
			}
		})
	}
}
