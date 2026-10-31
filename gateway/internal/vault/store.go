package vault

import (
	"database/sql"
	"errors"
	"time"
)

// ErrStoreNotFound is returned by GetStore when the key has no value yet.
var ErrStoreNotFound = errors.New("store key not found")

// PutStore stores an arbitrary client blob (currently the account sync
// payload) encrypted at rest. Callers are expected to keep values small;
// the server enforces a size limit before calling this.
func (v *Vault) PutStore(tenant, name, value string, now time.Time) error {
	enc, err := v.encrypt(value)
	if err != nil {
		return err
	}
	_, err = v.db.Exec(`INSERT INTO app_store (tenant, name, value, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (tenant, name) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		tenant, name, enc, now.Unix())
	return err
}

// GetStore returns the decrypted value for a key.
func (v *Vault) GetStore(tenant, name string) (string, error) {
	var enc string
	err := v.db.QueryRow(`SELECT value FROM app_store WHERE tenant = ? AND name = ?`, tenant, name).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrStoreNotFound
	}
	if err != nil {
		return "", err
	}
	return v.decrypt(enc)
}

// DeleteStore removes a key.
func (v *Vault) DeleteStore(tenant, name string) error {
	_, err := v.db.Exec(`DELETE FROM app_store WHERE tenant = ? AND name = ?`, tenant, name)
	return err
}
