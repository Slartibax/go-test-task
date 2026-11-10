package wallet

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

var (
	validateOnce sync.Once
	validateInst *validator.Validate
	errValidate  error
)

func rules() (*validator.Validate, error) {
	validateOnce.Do(func() {
		validateInst = validator.New()
		errValidate = validateInst.RegisterValidation("uuid_nz", uuidNotNil)
	})
	if errValidate != nil {
		return nil, fmt.Errorf("configure validator: %w", errValidate)
	}
	return validateInst, nil
}

func uuidNotNil(fl validator.FieldLevel) bool {
	id, ok := reflect.TypeAssert[uuid.UUID](fl.Field())
	return ok && id != uuid.Nil
}

func ValidateWalletID(id uuid.UUID) error {
	v, err := rules()
	if err != nil {
		return err
	}
	err = v.Struct(struct {
		WalletID uuid.UUID `validate:"uuid_nz"`
	}{WalletID: id})
	if err != nil {
		return mapValidationError(err)
	}
	return nil
}

func validateOperation(operation Operation) error {
	v, err := rules()
	if err != nil {
		return err
	}
	if err = v.Struct(operation); err != nil {
		return mapValidationError(err)
	}
	return nil
}

func mapValidationError(err error) error {
	var fields validator.ValidationErrors
	if !errors.As(err, &fields) {
		return fmt.Errorf("validate: %w", err)
	}

	var (
		found error
		rank  = 3
	)
	for _, field := range fields {
		nextRank, ok, next := domainError(field.Field())
		if ok && nextRank < rank {
			found = next
			rank = nextRank
		}
	}
	if found == nil {
		return fmt.Errorf("validate: %w", err)
	}
	return found
}

func domainError(field string) (int, bool, error) {
	switch field {
	case "WalletID":
		return 0, true, ErrInvalidWalletID
	case "Amount":
		return 1, true, ErrInvalidAmount
	case "OperationType":
		return 2, true, ErrInvalidOperation
	default:
		return 0, false, nil
	}
}
