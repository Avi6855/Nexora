package cassandra

import "github.com/google/uuid"

// UID converts a google/uuid.UUID into the canonical string form so gocql can
// marshal it into a CQL uuid column. gocql's uuid marshaler accepts strings
// and [16]byte but NOT github.com/google/uuid.UUID (a named [16]byte array),
// so binding it directly fails with "can not marshal uuid.UUID into uuid".
//
// Call this at every bind site:
//
//	query := `SELECT ... FROM users WHERE user_id = ?`
//	session.Query(query, cassandra.UID(id))...
func UID(id uuid.UUID) string {
	return id.String()
}

// UIDOrNil returns the canonical string form, or nil when the UUID is the
// zero value, so gocql binds a NULL instead of a zero UUID. Use it for
// optional UUID columns (e.g. payment.UserID before a user is attached).
func UIDOrNil(id uuid.UUID) interface{} {
	if id == (uuid.UUID{}) {
		return nil
	}
	return id.String()
}