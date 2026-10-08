package setting

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
)

// ExistingKeyForBackup never creates or changes a key. Validate against the
// completed snapshot so a rotated or missing key cannot produce false success.
func ExistingKeyForBackup(snapshot *sql.DB, keyFile string) ([]byte, error) {
	key := environmentKey()
	if key == nil {
		var err error
		key, err = readBackupKeyFile(keyFile)
		if err != nil {
			return nil, err
		}
	}
	if err := validateKey(snapshot, key); err != nil {
		return nil, err
	}
	return key, nil
}

// ValidateBackupKey explicitly uses the bundled key, ignoring GK_SECRET_KEY.
// This verifies portability to another machine without the source environment.
func ValidateBackupKey(snapshot *sql.DB, keyFile string) error {
	key, err := readBackupKeyFile(keyFile)
	if err != nil {
		return err
	}
	return validateKey(snapshot, key)
}

func readBackupKeyFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("备份密钥文件格式异常，需要原有效密钥")
	}
	return key, nil
}
