package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"arian-statement-parser/internal/client"
	"arian-statement-parser/internal/domain"
	pb "arian-statement-parser/internal/gen/arian/v1"
	"arian-statement-parser/internal/mapping"
	"arian-statement-parser/internal/parser"

	"github.com/joho/godotenv"
)

func convertToAccountType(accountType string) pb.AccountType {
	switch accountType {
	case "visa":
		return pb.AccountType_ACCOUNT_CREDIT_CARD
	case "savings":
		return pb.AccountType_ACCOUNT_SAVINGS
	case "chequing":
		return pb.AccountType_ACCOUNT_CHEQUING
	default:
		return pb.AccountType_ACCOUNT_UNSPECIFIED
	}
}

func findMatchingAccount(accounts []*pb.Account, accountName string, accountType string) *pb.Account {
	expectedType := convertToAccountType(accountType)
	for _, account := range accounts {
		if account.Type == expectedType && strings.EqualFold(account.Name, accountName) {
			return account
		}
	}
	return nil
}

func main() {
	pdfPath := flag.String("pdf", "", "")
	csvPath := flag.String("csv", "", "Optional RBC CSV export file to merge with statements")
	configPath := flag.String("config", "", "")
	flag.Parse()

	godotenv.Load()

	// Allow either PDF or CSV (or both)
	if *pdfPath == "" {
		if envPath := os.Getenv("PDF_PATH"); envPath != "" {
			*pdfPath = envPath
		} else if *csvPath == "" {
			fmt.Fprintf(os.Stderr, "need -pdf or -csv flag\n")
			os.Exit(1)
		}
	}

	userID := os.Getenv("USER_ID")
	if userID == "" {
		fmt.Fprintf(os.Stderr, "need USER_ID\n")
		os.Exit(1)
	}

	serverURL := os.Getenv("ARIAND_URL")
	if serverURL == "" {
		fmt.Fprintf(os.Stderr, "need ARIAND_URL\n")
		os.Exit(1)
	}

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "need API_KEY\n")
		os.Exit(1)
	}

	var parseResult *parser.ParseResult
	var transactions []*domain.Transaction

	// Parse PDF statements if provided
	if *pdfPath != "" {
		pythonParser := parser.NewPythonParser()

		fmt.Printf("parsing %s\n", *pdfPath)
		var err error
		parseResult, transactions, err = pythonParser.ParseStatements(*pdfPath, *configPath)
		if err != nil {
			log.Fatalf("parse failed: %v", err)
		}

		fmt.Printf("files: %d/%d, transactions: %d\n",
			parseResult.Summary.ProcessedFiles,
			parseResult.Summary.TotalFiles,
			parseResult.Summary.TotalTransactions)

		for _, fileResult := range parseResult.FileResults {
			fileName := filepath.Base(fileResult.File)
			if fileResult.Processed {
				fmt.Printf("  %s: %d\n", fileName, fileResult.TransactionCount)
			}
		}
	}

	// Parse and merge CSV file if provided
	if *csvPath != "" {
		csvParser := parser.NewCSVParser()
		fmt.Printf("\nparsing CSV %s\n", *csvPath)
		csvTransactions, err := csvParser.ParseCSV(*csvPath)
		if err != nil {
			log.Fatalf("CSV parse failed: %v", err)
		}

		fmt.Printf("CSV transactions: %d\n", len(csvTransactions))

		// Merge with smart deduplication
		originalCount := len(transactions)
		transactions = parser.MergeCSVWithStatements(transactions, csvTransactions)
		newCount := len(transactions) - originalCount

		fmt.Printf("merged: %d new from CSV (after deduplication)\n", newCount)
	}

	if len(transactions) == 0 {
		return
	}

	fmt.Printf("\nupload %d transactions? (y/N): ", len(transactions))
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		log.Fatalf("read failed: %v", err)
	}

	response = strings.TrimSpace(strings.ToLower(response))
	if response != "y" && response != "yes" {
		return
	}

	arianClient, err := client.NewClient(serverURL, "", apiKey)
	if err != nil {
		log.Fatalf("client failed: %v", err)
	}
	defer arianClient.Close()

	_, err = arianClient.GetUser(userID)
	if err != nil {
		log.Fatalf("user not found: %v", err)
	}

	accounts, err := arianClient.GetAccounts(userID)
	if err != nil {
		log.Fatalf("get accounts failed: %v", err)
	}

	// Initialize mapping store
	mappingStore, err := mapping.NewStore()
	if err != nil {
		log.Fatalf("failed to initialize mapping store: %v", err)
	}

	accountMatchStats := make(map[string]int)
	askedMappings := make(map[string]bool) // Track which accounts we've already asked about

	// First pass: resolve all account mappings
	for _, tx := range transactions {
		var accountName string
		if tx.StatementAccountNumber != nil && *tx.StatementAccountNumber != "" {
			accountName = *tx.StatementAccountNumber
		} else {
			accountName = "Unknown"
		}

		mappingKey := accountName + "|" + tx.StatementAccountType
		if askedMappings[mappingKey] {
			continue // Already resolved this account
		}
		askedMappings[mappingKey] = true

		var matchedAccount *pb.Account

		// First, check if we have a saved mapping for this statement account
		arianAccountName := mappingStore.FindMapping(accountName)

		if arianAccountName != "" {
			// Use the saved mapping - resolve by account name
			matchedAccount = mappingStore.ResolveAccount(arianAccountName, accounts)
			if matchedAccount == nil {
				log.Printf("WARN: saved mapping for '%s' points to non-existent account '%s', will re-prompt", accountName, arianAccountName)
			}
		}

		// If no saved mapping or account not found, try to match by name and type
		if matchedAccount == nil {
			matchedAccount = findMatchingAccount(accounts, accountName, tx.StatementAccountType)
		}

		// If still no match, prompt the user
		if matchedAccount == nil {
			selectedAccountID, isNewAccount, err := mapping.PromptForAccountMapping(accountName, accounts)
			if err != nil {
				log.Fatalf("mapping prompt failed: %v", err)
			}

			if isNewAccount {
				// Create new account
				accountType := convertToAccountType(tx.StatementAccountType)
				newAccount, err := arianClient.CreateAccount(userID, accountName, "RBC", accountType, "CAD")
				if err != nil {
					log.Fatalf("create account failed: %v", err)
				}
				matchedAccount = newAccount
				accounts = append(accounts, newAccount)

				// Save mapping
				err = mappingStore.AddMapping(accountName, newAccount.Name)
				if err != nil {
					log.Printf("WARN: failed to save mapping: %v", err)
				}
			} else {
				// Use selected existing account
				selectedAccountIDInt, _ := strconv.ParseInt(selectedAccountID, 10, 64)
				for _, account := range accounts {
					if account.Id == selectedAccountIDInt {
						matchedAccount = account
						break
					}
				}

				if matchedAccount == nil {
					log.Fatalf("selected account not found")
				}

				// Save mapping
				err = mappingStore.AddMapping(accountName, matchedAccount.Name)
				if err != nil {
					log.Printf("WARN: failed to save mapping: %v", err)
				}

				// Warn if types don't match
				expectedType := convertToAccountType(tx.StatementAccountType)
				if matchedAccount.Type != expectedType {
					log.Printf("WARN: account '%s' type mismatch - statement expects %s but account is %s (continuing anyway)", accountName, expectedType, matchedAccount.Type)
				}
			}
		}
	}

	// Second pass: assign account IDs to all transactions
	for _, tx := range transactions {
		var accountName string
		if tx.StatementAccountNumber != nil && *tx.StatementAccountNumber != "" {
			accountName = *tx.StatementAccountNumber
		} else {
			accountName = "Unknown"
		}

		arianAccountName := mappingStore.FindMapping(accountName)
		if arianAccountName == "" {
			// Try to match by name and type
			matchedAccount := findMatchingAccount(accounts, accountName, tx.StatementAccountType)
			if matchedAccount != nil {
				tx.AccountID = int(matchedAccount.Id)
				accountMatchStats[accountName]++
			} else {
				log.Fatalf("no account found for transaction with account '%s' (this shouldn't happen)", accountName)
			}
		} else {
			// Resolve account by name
			matchedAccount := mappingStore.ResolveAccount(arianAccountName, accounts)
			if matchedAccount != nil {
				tx.AccountID = int(matchedAccount.Id)
				accountMatchStats[accountName]++
			} else {
				log.Fatalf("no account found for mapping '%s' -> '%s' (this shouldn't happen)", accountName, arianAccountName)
			}
		}
	}

	// Bulk upload transactions in batches
	const batchSize = 1000
	totalCreated := int32(0)
	totalErrors := 0

	for i := 0; i < len(transactions); i += batchSize {
		end := i + batchSize
		if end > len(transactions) {
			end = len(transactions)
		}

		batch := transactions[i:end]
		created, errors := arianClient.CreateTransactionsBulk(userID, batch)
		totalCreated += created
		totalErrors += len(errors)

		if len(errors) > 0 {
			for _, err := range errors {
				log.Printf("ERROR: %v", err)
			}
		}

		fmt.Printf("%d/%d\n", end, len(transactions))
	}

	fmt.Printf("\n%d ok, %d failed\n", totalCreated, totalErrors)
	for account, count := range accountMatchStats {
		fmt.Printf("  %s: %d\n", account, count)
	}
}
