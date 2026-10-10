package setting

import (
	"context"
	"path/filepath"
	"testing"

	"ai_analyze_guokao/internal/store"
)

// Catches SMTP keys that fail to round-trip, mask, or appear in All() defaults.
func TestMailSettingKeys(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mail-setting.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := Open(db, filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = s.Set(ctx, KeySMTPPassword, "secret", "test"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get(ctx, KeySMTPPassword); err != nil || v != "secret" {
		t.Fatal(v, err)
	}
	if v, set, err := s.GetMasked(ctx, KeySMTPPassword); err != nil || v != Mask || !set {
		t.Fatal(v, set, err)
	}
	all, err := s.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := all["mail.smtp_host"]; !ok || v != "" {
		t.Fatal("smtp_host default missing", v, ok)
	}
	if err = s.Set(ctx, KeySMTPHost, "smtp.example.com", "test"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get(ctx, KeySMTPHost); err != nil || v != "smtp.example.com" {
		t.Fatal(v, err)
	}
	if v, set, err := s.GetMasked(ctx, KeySMTPHost); err != nil || v != "smtp.example.com" || !set {
		t.Fatal("non-secret key masked", v, set, err)
	}
}
