package oauth

import (
	"context"
	"testing"
)

func TestDiscoverAuthentik_ReadsEndpoints(t *testing.T) {
	fake := newFakeAuthentik(t, "sub-123")

	eps, err := discoverAuthentik(context.Background(), fake.URL, fake.Client())
	if err != nil {
		t.Fatalf("discoverAuthentik: %v", err)
	}
	if eps.Token != fake.URL+"/token" {
		t.Errorf("Token = %q, want %q", eps.Token, fake.URL+"/token")
	}
	if eps.UserInfo != fake.URL+"/userinfo" {
		t.Errorf("UserInfo = %q, want %q", eps.UserInfo, fake.URL+"/userinfo")
	}
}

func TestExchangeAndIdentify(t *testing.T) {
	fake := newFakeAuthentik(t, "sub-123")
	srv := newTestServer(t)
	srv.httpClient = fake.Client()
	eps, err := discoverAuthentik(context.Background(), fake.URL, fake.Client())
	if err != nil {
		t.Fatal(err)
	}
	srv.authentik = eps

	t.Run("valid code yields the subject", func(t *testing.T) {
		sub, err := srv.exchangeAndIdentify(context.Background(), "good-code")
		if err != nil {
			t.Fatalf("exchangeAndIdentify: %v", err)
		}
		if sub != "sub-123" {
			t.Errorf("sub = %q, want sub-123", sub)
		}
	})

	t.Run("rejected code surfaces an error", func(t *testing.T) {
		if _, err := srv.exchangeAndIdentify(context.Background(), "wrong-code"); err == nil {
			t.Error("want error for a code authentik rejects")
		}
	})
}
