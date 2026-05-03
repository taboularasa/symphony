package phase1

import "fmt"

type OwnerLabel struct {
	Name        string
	Color       string
	Description string
}

var ReservedOwnerLabels = []OwnerLabel{
	{
		Name:        "owner:hermes",
		Color:       "#4F46E5",
		Description: "Symphony owner routing: Hermes automation owns this issue.",
	},
	{
		Name:        "owner:denovo",
		Color:       "#0F766E",
		Description: "Symphony owner routing: De Novo automation owns this issue.",
	},
	{
		Name:        "owner:human",
		Color:       "#64748B",
		Description: "Symphony owner routing: human owner only; automation must not claim.",
	},
	{
		Name:        "owner:triage",
		Color:       "#B45309",
		Description: "Symphony owner routing: needs human or Hermes triage before ownership.",
	},
}

func OwnerLabelByName(name string) (OwnerLabel, bool) {
	for _, label := range ReservedOwnerLabels {
		if label.Name == name {
			return label, true
		}
	}
	return OwnerLabel{}, false
}

func ValidateOwnerLabel(name string) error {
	if _, ok := OwnerLabelByName(name); ok {
		return nil
	}
	return fmt.Errorf("unknown owner label %q", name)
}
