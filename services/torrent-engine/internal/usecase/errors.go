package usecase

import (
	"errors"
	"fmt"
)

var (
	ErrEngine           = errors.New("engine error")
	ErrRepository       = errors.New("repository error")
	ErrInvalidFileIndex = errors.New("invalid file index")
)

func wrapEngine(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrEngine, err)
}

func wrapRepo(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrRepository, err)
}
