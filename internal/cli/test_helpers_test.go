package cli

import "errors"

func assertErr(message string) error {
	return errors.New(message)
}
