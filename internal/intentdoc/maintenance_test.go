package intentdoc

import "testing"

func validMaintenance() Maintenance {
	interval := uint32(60)
	return Maintenance{
		Triggers: []MaintenanceTrigger{{Key: "manual-check", Kind: "manual"}, {Key: "heartbeat", Kind: "interval", IntervalSeconds: &interval}},
		Budget:   MaintenanceBudget{MaxIterations: 4, MaxTasks: 20, MaxElapsedSeconds: 3600, NoProgressLimit: 3},
	}
}

func TestValidateMaintenance(t *testing.T) {
	if err := validateMaintenance(validMaintenance()); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*Maintenance)
	}{
		{"no triggers", func(m *Maintenance) { m.Triggers = nil }},
		{"duplicate key", func(m *Maintenance) { m.Triggers = append(m.Triggers, m.Triggers[0]) }},
		{"invalid key", func(m *Maintenance) { m.Triggers[0].Key = "Bad Key" }},
		{"unsupported kind", func(m *Maintenance) { m.Triggers[0].Kind = "cron" }},
		{"manual interval", func(m *Maintenance) { v := uint32(1); m.Triggers[0].IntervalSeconds = &v }},
		{"interval missing", func(m *Maintenance) { m.Triggers[0].Kind = "interval" }},
		{"interval zero", func(m *Maintenance) { v := uint32(0); m.Triggers[1].IntervalSeconds = &v }},
		{"zero iterations", func(m *Maintenance) { m.Budget.MaxIterations = 0 }},
		{"zero tasks", func(m *Maintenance) { m.Budget.MaxTasks = 0 }},
		{"zero elapsed", func(m *Maintenance) { m.Budget.MaxElapsedSeconds = 0 }},
		{"zero progress", func(m *Maintenance) { m.Budget.NoProgressLimit = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validMaintenance()
			tc.mutate(&m)
			if err := validateMaintenance(m); err == nil {
				t.Fatal("invalid maintenance accepted")
			}
		})
	}
}
