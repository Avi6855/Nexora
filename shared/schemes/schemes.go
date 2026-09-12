// Package schemes implements scheme certification, routing, settlement
// windows and fee attribution for card-network simulation.
package schemes

import "errors"

var (
	ErrSchemeInvalidInput = errors.New("invalid scheme input")
	ErrCertCaseNotFound   = errors.New("certification case not found")
	ErrCertRunNotFound    = errors.New("certification run not found")
	ErrRailExists         = errors.New("rail already exists")
	ErrRailNotFound       = errors.New("rail not found")
	ErrNoRoute            = errors.New("no route available")
	ErrSchemeNotFound     = errors.New("scheme not found")
	ErrFeeInvalid         = errors.New("invalid fee breakdown")
)
