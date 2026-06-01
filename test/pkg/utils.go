package pkg

// PtrStringOrDefault safely dereferences a *string for display purposes.
// Returns the pointed-to value, or defaultVal if the pointer is nil.
func PtrStringOrDefault(s *string, defaultVal string) string {
	if s == nil {
		return defaultVal
	}
	return *s
}
