package syncer

import "fmt"

type MissingEnvironmentVariablesError struct {
	missingVariables []string
	message          string
}

func (err *MissingEnvironmentVariablesError) Error() string {
	return err.message
}

func NewMissingEnvironmentVariablesError(variables []string) *MissingEnvironmentVariablesError {
	message := "the following environment variables are missing or empty: "
	for i := range variables {
		if lastIndex := len(variables) - 1; i != lastIndex {
			message += fmt.Sprintf("%s, ", variables[i])
			continue
		}
		message += fmt.Sprintf("%s", variables[i])
	}
	return &MissingEnvironmentVariablesError{
		missingVariables: variables,
		message:          message,
	}
}
