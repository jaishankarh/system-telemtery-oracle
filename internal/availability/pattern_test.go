package availability_test

import (
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/availability"
	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// This is the pattern a new data-source ticket follows: describe a file it
// reads, probe something it detects, and name the Advice rule that needs it.
// Nothing here edits a shared file; it all happens in the source's own init.
func init() {
	availability.Describe("/proc/fake-feature", availability.Item{ID: "fake_feature", Feature: "Fake feature",
		Reason: "the kernel has no /proc/fake-feature", Affects: []string{"Fake view"}})
	availability.RegisterProbe("fake_device", func(m machine.Machine, _ availability.Environment) *availability.Item {
		if len(m.Glob("/sys/fake/device")) > 0 {
			return nil
		}
		return &availability.Item{ID: "fake_device", Feature: "Fake device", Reason: "no fake device found"}
	})
	// A source ticket can also replace a default probe by registering the same id:
	availability.RegisterProbe("gpu", func(machine.Machine, availability.Environment) *availability.Item { return nil })
}

func TestARegisteredSourceIsListedAndDisablesItsRule(t *testing.T) {
	m := harness.NewModern(t)
	m.WithoutGPU()
	p := m.Start()
	var a struct {
		Unavailable []struct{ ID, Feature, Reason string } `json:"unavailable"`
	}
	p.GetJSON("/api/availability", &a)
	var ids []string
	for _, u := range a.Unavailable {
		ids = append(ids, u.ID)
	}
	if got := strings.Join(ids, ","); got != "fake_device" { // the gpu probe was replaced; fake_feature's file is not read
		t.Fatalf("unavailable = %q", got)
	}
	why := availability.DisabledReason([]availability.Item{{ID: "fake_device", Feature: "Fake device", Reason: "no fake device found"}}, "fake_device")
	if why != "needs Fake device (no fake device found)" {
		t.Fatalf("reason = %q", why)
	}
}
