package keyringstore

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const service = "ztime-cli"

func account(deviceID string) string {
	if deviceID == "" {
		return "device-key"
	}
	return "device-key:" + deviceID
}

func SetDevicePrivateKey(deviceID, privateKey string) error {
	if privateKey == "" {
		return fmt.Errorf("empty device private key")
	}
	return keyring.Set(service, account(deviceID), privateKey)
}

func GetDevicePrivateKey(deviceID string) (string, error) {
	return keyring.Get(service, account(deviceID))
}

func DeleteDevicePrivateKey(deviceID string) error {
	err := keyring.Delete(service, account(deviceID))
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}

func IsNotFound(err error) bool {
	return errors.Is(err, keyring.ErrNotFound)
}
