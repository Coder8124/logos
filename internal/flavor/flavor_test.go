package flavor

import "testing"

func TestLoadOnFreshVault(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "" || cfg.UserName != "" {
		t.Errorf("fresh vault should be unnamed, got %+v", cfg)
	}
	if cfg.Onboarded {
		t.Error("a fresh vault has not been onboarded — the welcome screen depends on this")
	}
}

func TestRoundTripPreservesIdentityAndPresence(t *testing.T) {
	dir := t.TempDir()
	c := &Config{
		Name:      "Kestrel",
		UserName:  "Pragun",
		Onboarded: true,
		Presence:  Presence{Interjections: true, MeetingLeadMinutes: 15, QuietHours: []string{"22:00", "08:00"}},
	}
	if err := c.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Kestrel" || got.UserName != "Pragun" || !got.Onboarded {
		t.Errorf("identity lost in round trip: %+v", got)
	}
	if got.Presence.MeetingLeadMinutes != 15 || len(got.Presence.QuietHours) != 2 {
		t.Errorf("presence lost in round trip: %+v", got.Presence)
	}
}

func TestPresenceDefaultsFillOnlyUnsetFields(t *testing.T) {
	got := Presence{MeetingLeadMinutes: 3}.WithDefaults()
	if got.MeetingLeadMinutes != 3 {
		t.Errorf("an explicit lead time must survive defaulting, got %d", got.MeetingLeadMinutes)
	}
	if got.MinGapMinutes != 60 {
		t.Errorf("unset gap should default to 60, got %d", got.MinGapMinutes)
	}
}
