// Package testgrid implements synthetic regression generation, chaos
// policy evaluation and journey×fault grids for simulation testing.
package testgrid

import "errors"

var (
	ErrGridInvalidInput = errors.New("invalid testgrid input")
	ErrUnknownIncident  = errors.New("unknown incident kind")
	ErrScenarioNotFound = errors.New("scenario not found")
	ErrJourneyNotFound  = errors.New("journey not found")
	ErrJourneyNotRun    = errors.New("journey grid not run yet")
)
