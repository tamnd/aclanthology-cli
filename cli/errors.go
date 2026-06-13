package cli

import (
	"errors"

	"github.com/tamnd/aclanthology-cli/aclanthology"
)

func isNotFound(err error) bool {
	return errors.Is(err, aclanthology.ErrNotFound)
}
