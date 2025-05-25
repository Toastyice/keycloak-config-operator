package controller

// slicesEqual compares two string slices, treating nil and empty slices as equal
func slicesEqual(a, b []string) bool {
	// Handle nil vs empty slice equivalence
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

func stringPtr(s string) *string {
	return &s
}

func derefStringPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
