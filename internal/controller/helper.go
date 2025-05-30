// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

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
