package repository

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/identity-service/internal/domain"
)

type DeviceRepository interface {
	Create(ctx context.Context, device *domain.Device) error
	GetByID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (*domain.Device, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*domain.Device, error)
	UpdateLastActive(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) error
	UpdateFCMToken(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID, fcmToken string) error
}

type cassandraDeviceRepository struct {
	session *gocql.Session
}

func NewCassandraDeviceRepository(session *gocql.Session) DeviceRepository {
	return &cassandraDeviceRepository{session: session}
}

func (r *cassandraDeviceRepository) Create(ctx context.Context, device *domain.Device) error {
	query := `INSERT INTO devices (user_id, device_id, device_name, device_type, fcm_token, last_active, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	return r.session.Query(query,
		gocql.UUID(device.UserID),
		gocql.UUID(device.DeviceID),
		device.DeviceName,
		string(device.DeviceType),
		device.FCMToken,
		device.LastActive,
		device.CreatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraDeviceRepository) GetByID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (*domain.Device, error) {
	var device domain.Device
	var uid, did gocql.UUID

	query := `SELECT user_id, device_id, device_name, device_type, fcm_token, last_active, created_at
		FROM devices WHERE user_id = ? AND device_id = ?`

	err := r.session.Query(query, gocql.UUID(userID), gocql.UUID(deviceID)).WithContext(ctx).Scan(
		&uid,
		&did,
		&device.DeviceName,
		&device.DeviceType,
		&device.FCMToken,
		&device.LastActive,
		&device.CreatedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	device.UserID = uuid.UUID(uid)
	device.DeviceID = uuid.UUID(did)
	return &device, nil
}

func (r *cassandraDeviceRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*domain.Device, error) {
	var devices []*domain.Device

	query := `SELECT user_id, device_id, device_name, device_type, fcm_token, last_active, created_at
		FROM devices WHERE user_id = ?`

	iter := r.session.Query(query, gocql.UUID(userID)).WithContext(ctx).Iter()
	defer iter.Close()

	var device domain.Device
	var uid, did gocql.UUID
	for iter.Scan(
		&uid,
		&did,
		&device.DeviceName,
		&device.DeviceType,
		&device.FCMToken,
		&device.LastActive,
		&device.CreatedAt,
	) {
		device.UserID = uuid.UUID(uid)
		device.DeviceID = uuid.UUID(did)
		d := device
		devices = append(devices, &d)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}

	return devices, nil
}

func (r *cassandraDeviceRepository) UpdateLastActive(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) error {
	now := time.Now().UTC()
	query := `UPDATE devices SET last_active = ? WHERE user_id = ? AND device_id = ?`
	return r.session.Query(query, now, gocql.UUID(userID), gocql.UUID(deviceID)).WithContext(ctx).Exec()
}

func (r *cassandraDeviceRepository) UpdateFCMToken(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID, fcmToken string) error {
	query := `UPDATE devices SET fcm_token = ? WHERE user_id = ? AND device_id = ?`
	return r.session.Query(query, fcmToken, gocql.UUID(userID), gocql.UUID(deviceID)).WithContext(ctx).Exec()
}
