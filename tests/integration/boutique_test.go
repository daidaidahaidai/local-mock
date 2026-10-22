package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBoutiqueCheckoutSuccessAndPaymentFailure(t *testing.T) {
	if os.Getenv("LOCALMOCK_BOUTIQUE_TEST") != "1" {
		t.Skip("set LOCALMOCK_BOUTIQUE_TEST=1 after preparing the pinned upstream checkout")
	}
	h := setup(t)
	for _, test := range []struct {
		name, file string
		success    bool
	}{{"success", "scenario.yaml", true}, {"payment-failure", "payment-failure.yaml", false}} {
		t.Run(test.name, func(t *testing.T) {
			r := h.start(filepath.Join(h.root, "examples/online-boutique", test.file))
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, h.bin, "probe", "--state", h.state, "--deployment", "checkout", "--interface", "grpc", "--method", "hipstershop.CheckoutService/PlaceOrder", "--data-file", filepath.Join(h.root, "examples/online-boutique/request.json"), r.ID)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, callErr := cmd.Output()
			var result struct {
				TraceID  string `json:"trace_id"`
				Code     string `json:"code"`
				Response struct {
					Order struct {
						Tracking string            `json:"shippingTrackingId"`
						Items    []json.RawMessage `json:"items"`
					} `json:"order"`
				} `json:"response"`
			}
			if err := json.Unmarshal(out, &result); err != nil {
				t.Fatalf("rpc output %s stderr %s: %v", out, stderr.String(), err)
			}
			if test.success {
				if callErr != nil || result.Code != "OK" || result.Response.Order.Tracking != "local-shipment-001" || len(result.Response.Order.Items) != 1 {
					t.Fatalf("unexpected success response: %s %s %v", out, stderr.String(), callErr)
				}
			} else {
				if callErr == nil || result.Code != "Internal" || !strings.Contains(stderr.String(), "simulated payment outage") {
					t.Fatalf("unexpected payment failure: %s %s %v", out, stderr.String(), callErr)
				}
			}
			logs, err := h.command("logs", r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(logs, []byte("hipstershop.PaymentService/Charge")) {
				t.Fatal("missing payment evidence")
			}
			if !bytes.Contains(logs, []byte(result.TraceID)) {
				t.Fatal("request trace ID was not propagated to any mock")
			}
			if !test.success && (bytes.Contains(logs, []byte("hipstershop.ShippingService/ShipOrder")) || bytes.Contains(logs, []byte("hipstershop.EmailService/SendOrderConfirmation"))) {
				t.Fatal("downstream side effects happened after payment failure")
			}
			if _, err := h.command("down", r.ID); err != nil {
				t.Fatal(err)
			}
			h.assertRemoved(r.ID)
		})
	}
}
