// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package controller

// FieldDiff represents a difference between expected and actual field values
type FieldDiff struct {
	Name string
	Old  any
	New  any
}
