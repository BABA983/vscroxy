package capture

import "testing"

func TestRuleFolderValidation(t *testing.T) {
	valid := []string{"", "API", "API/Auth", "API/HTTP v2"}
	for _, folder := range valid {
		if err := ValidateRules([]Rule{{Type: RuleBlock, Match: "/x", Folder: folder}}); err != nil {
			t.Fatalf("folder %q: %v", folder, err)
		}
	}
	deep := "a/b/c/d/e/f/g/h/i"
	long := ""
	for range 65 {
		long += "x"
	}
	invalid := []string{" API", "API/", "/API", "API//Auth", "API/\tAuth", "API/ Auth", deep, long}
	for _, folder := range invalid {
		if err := ValidateRules([]Rule{{Type: RuleBlock, Match: "/x", Folder: folder}}); err == nil {
			t.Fatalf("accepted folder %q", folder)
		}
	}
}
