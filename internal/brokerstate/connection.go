package brokerstate

import (
	"database/sql"
	"database/sql/driver"
	"errors"
)

func discard(conn *sql.Conn) error {
	err := conn.Raw(func(any) error { return driver.ErrBadConn })
	if errors.Is(err, driver.ErrBadConn) {
		return nil
	}
	return err
}
