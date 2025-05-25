package controller

// FieldDiff represents a difference between expected and actual field values
type FieldDiff struct {
	Name string
	Old  any
	New  any
}
