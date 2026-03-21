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

	"null-statement-parser/internal/client"
	"null-statement-parser/internal/domain"
	pb "null-statement-parser/internal/gen/null/v1"
	"null-statement-parser/internal/mapping"
	"null-statement-parser/internal/parser"

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

	accountMatchStats := make(map[string]int)
	// resolvedAccounts caches alias -> account lookups within this run
	resolvedAccounts := make(map[string]*pb.Account)

	// First pass: resolve all account mappings via API aliases
	askedMappings := make(map[string]bool)
	for _, tx := range transactions {
		var accountName string
		if tx.StatementAccountNumber != nil && *tx.StatementAccountNumber != "" {
			accountName = *tx.StatementAccountNumber
		} else {
			accountName = "Unknown"
		}

		mappingKey := accountName + "|" + tx.StatementAccountType
		if askedMappings[mappingKey] {
			continue
		}
		askedMappings[mappingKey] = true

		// Check API for alias
		matchedAccount, err := arianClient.FindAccountByAlias(accountName)
		if err != nil {
			// Not found — prompt user
			selectedAccountID, isNewAccount, err := mapping.PromptForAccountMapping(accountName, accounts)
			if err != nil {
				log.Fatalf("mapping prompt failed: %v", err)
			}

			if isNewAccount {
				accountType := convertToAccountType(tx.StatementAccountType)
				newAccount, err := arianClient.CreateAccount(userID, accountName, "RBC", accountType, "CAD")
				if err != nil {
					log.Fatalf("create account failed: %v", err)
				}
				matchedAccount = newAccount
				accounts = append(accounts, newAccount)
			} else {
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
				expectedType := convertToAccountType(tx.StatementAccountType)
				if matchedAccount.Type != expectedType {
					log.Printf("WARN: account '%s' type mismatch - statement expects %s but account is %s (continuing anyway)", accountName, expectedType, matchedAccount.Type)
				}
			}

			if err := arianClient.AddAccountAlias(matchedAccount.Id, accountName); err != nil {
				log.Printf("WARN: failed to add alias: %v", err)
			}
		}

		resolvedAccounts[accountName] = matchedAccount
	}

	// Second pass: assign account IDs to all transactions
	for _, tx := range transactions {
		var accountName string
		if tx.StatementAccountNumber != nil && *tx.StatementAccountNumber != "" {
			accountName = *tx.StatementAccountNumber
		} else {
			accountName = "Unknown"
		}

		matchedAccount := resolvedAccounts[accountName]
		if matchedAccount == nil {
			log.Fatalf("no account resolved for '%s' (this shouldn't happen)", accountName)
		}
		tx.AccountID = int(matchedAccount.Id)
		accountMatchStats[accountName]++
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
