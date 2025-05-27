package controller

func stringPtr(s string) *string {
	return &s
}

func derefStringPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
