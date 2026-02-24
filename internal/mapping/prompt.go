package mapping

import (
	"fmt"
	"strconv"

	pb "null-statement-parser/internal/gen/null/v1"

	"github.com/charmbracelet/huh"
)

const (
	OptionNewAccount = "__new_account__"
)

// PromptForAccountMapping prompts the user to map a statement account to an existing ariand account
func PromptForAccountMapping(statementAccountNumber string, existingAccounts []*pb.Account) (string, bool, error) {
	var selectedOption string
	isNewAccount := false

	// Build options list
	options := make([]huh.Option[string], 0, len(existingAccounts)+1)

	// Add "Create new account" option first
	options = append(options, huh.NewOption(
		"Create new account",
		OptionNewAccount,
	))

	// Add existing accounts
	for _, account := range existingAccounts {
		label := fmt.Sprintf("%s (%s - %s)", account.Name, account.Bank, account.Type.String())
		options = append(options, huh.NewOption(label, strconv.FormatInt(account.Id, 10)))
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(fmt.Sprintf("Found account '%s' in statement", statementAccountNumber)).
				Description("Map this to:").
				Options(options...).
				Value(&selectedOption),
		),
	)

	err := form.Run()
	if err != nil {
		return "", false, fmt.Errorf("prompt failed: %w", err)
	}

	if selectedOption == OptionNewAccount {
		isNewAccount = true
		return "", isNewAccount, nil
	}

	return selectedOption, isNewAccount, nil
}
