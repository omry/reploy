package dockerdeploy

import "errors"

type providerHelperCleanupError struct {
	err error
}

func (failure *providerHelperCleanupError) Error() string { return failure.err.Error() }
func (failure *providerHelperCleanupError) Unwrap() error { return failure.err }

func markProviderHelperCleanupError(err error) error {
	if err == nil {
		return nil
	}
	return &providerHelperCleanupError{err: err}
}

func providerHelperCleanupFailed(err error) bool {
	var failure *providerHelperCleanupError
	return errors.As(err, &failure)
}
