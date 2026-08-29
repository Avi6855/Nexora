package domain

import (
	"time"

	"github.com/google/uuid"
)

type DeviceType string

const (
	DeviceTypeAndroid DeviceType = "ANDROID"
	DeviceTypeIOS     DeviceType = "IOS"
	DeviceTypeWeb     DeviceType = "WEB"
)

type Device struct {
	DeviceID   uuid.UUID  `json:"device_id"`
	UserID     uuid.UUID  `json:"user_id"`
	DeviceName string     `json:"device_name"`
	DeviceType DeviceType `json:"device_type"`
	FCMToken   string     `json:"fcm_token,omitempty"`
	LastActive time.Time  `json:"last_active"`
	CreatedAt  time.Time  `json:"created_at"`
}

func NewDevice(userID uuid.UUID, name string, deviceType DeviceType, fcmToken string) *Device {
	now := time.Now().UTC()
	return &Device{
		DeviceID:   uuid.New(),
		UserID:     userID,
		DeviceName: name,
		DeviceType: deviceType,
		FCMToken:   fcmToken,
		LastActive: now,
		CreatedAt:  now,
	}
}
