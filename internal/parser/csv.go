package parser

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"arian-statement-parser/internal/domain"
)

type CSVParser struct{}

func NewCSVParser() *CSVParser {
	return &CSVParser{}
}

// ParseCSV parses RBC CSV export file
// CSV format: "Account Type","Account Number","Transaction Date","Cheque Number","Description 1","Description 2","CAD$","USD$"
func (p *CSVParser) ParseCSV(csvPath string) ([]*domain.Transaction, error) {
	file, err := os.Open(csvPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open CSV file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1 // Allow variable number of fields
	reader.TrimLeadingSpace = true

	// Read all records
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV: %w", err)
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("CSV file is empty or has no data rows")
	}

	// Parse header to find column indices
	header := records[0]
	colIndices := make(map[string]int)
	for i, col := range header {
		colIndices[col] = i
	}

	// Validate required columns
	requiredCols := []string{"Account Type", "Account Number", "Transaction Date", "Description 1", "CAD$", "USD$"}
	for _, col := range requiredCols {
		if _, ok := colIndices[col]; !ok {
			return nil, fmt.Errorf("missing required column: %s", col)
		}
	}

	var transactions []*domain.Transaction

	// Parse each row (skip header)
	for i, record := range records[1:] {
		tx, err := p.parseCSVRow(record, colIndices, csvPath)
		if err != nil {
			// Skip malformed rows with a warning
			fmt.Printf("Warning: skipping row %d: %v\n", i+2, err)
			continue
		}
		if tx != nil {
			transactions = append(transactions, tx)
		}
	}

	return transactions, nil
}

func (p *CSVParser) parseCSVRow(record []string, colIndices map[string]int, sourcePath string) (*domain.Transaction, error) {
	// Helper to safely get column value
	getCol := func(name string) string {
		if idx, ok := colIndices[name]; ok && idx < len(record) {
			return strings.TrimSpace(record[idx])
		}
		return ""
	}

	// Parse account type
	accountType := strings.ToLower(getCol("Account Type"))
	if accountType == "" {
		return nil, fmt.Errorf("empty account type")
	}

	// Parse account number
	accountNumber := getCol("Account Number")
	if accountNumber == "" {
		return nil, fmt.Errorf("empty account number")
	}

	// Parse transaction date (format: M/D/YYYY or MM/DD/YYYY)
	dateStr := getCol("Transaction Date")
	txDate, err := time.Parse("1/2/2006", dateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid date format: %s", dateStr)
	}

	// Parse descriptions
	desc1 := getCol("Description 1")
	desc2 := getCol("Description 2")
	description := strings.TrimSpace(desc1 + " " + desc2)
	if description == "" {
		return nil, fmt.Errorf("empty description")
	}

	// Parse amount (CAD$ or USD$)
	cadStr := getCol("CAD$")
	usdStr := getCol("USD$")

	var amount float64
	var currency string

	if cadStr != "" {
		cadStr = strings.ReplaceAll(cadStr, ",", "")
		amount, err = strconv.ParseFloat(cadStr, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid CAD amount: %s", cadStr)
		}
		currency = "CAD"
	} else if usdStr != "" {
		usdStr = strings.ReplaceAll(usdStr, ",", "")
		amount, err = strconv.ParseFloat(usdStr, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid USD amount: %s", usdStr)
		}
		currency = "USD"
	} else {
		return nil, fmt.Errorf("no amount specified")
	}

	if amount == 0 {
		return nil, nil // Skip zero-amount transactions
	}

	// Determine direction
	var direction domain.Direction
	if amount < 0 {
		direction = domain.Out
		amount = -amount
	} else {
		direction = domain.In
	}

	// Normalize account type for matching
	normalizedAccountType := accountType
	if accountType == "chequing" || accountType == "savings" {
		normalizedAccountType = accountType
	} else if accountType == "visa" {
		normalizedAccountType = "visa"
	}

	return &domain.Transaction{
		TxDate:                 txDate,
		TxAmount:               amount,
		TxCurrency:             currency,
		TxDirection:            direction,
		TxDesc:                 description,
		StatementAccountNumber: &accountNumber,
		StatementAccountType:   normalizedAccountType,
		StatementAccountName:   "", // CSV doesn't have account name
		SourceFilePath:         sourcePath,
	}, nil
}

// GetLast4Digits extracts the last 4 digits from an account number
func GetLast4Digits(accountNumber string) string {
	if len(accountNumber) >= 4 {
		return accountNumber[len(accountNumber)-4:]
	}
	return accountNumber
}

// MatchesAccount checks if a transaction's account number matches the given last 4 digits
func MatchesAccount(tx *domain.Transaction, last4 string) bool {
	if tx.StatementAccountNumber == nil {
		return false
	}
	accountNum := *tx.StatementAccountNumber
	// Check if it ends with last4 or equals last4
	return strings.HasSuffix(accountNum, last4) || accountNum == last4
}

// FindLatestTransactionDate finds the latest transaction date for a specific account
func FindLatestTransactionDate(transactions []*domain.Transaction, accountLast4 string) *time.Time {
	var latest *time.Time

	for _, tx := range transactions {
		if MatchesAccount(tx, accountLast4) {
			if latest == nil || tx.TxDate.After(*latest) {
				latest = &tx.TxDate
			}
		}
	}

	return latest
}

// MergeCSVWithStatements merges CSV transactions with statement transactions, avoiding duplicates
// For each account in the CSV:
// 1. Find the latest transaction date in the statements
// 2. Only include CSV transactions that are AFTER that date
func MergeCSVWithStatements(statementTxs []*domain.Transaction, csvTxs []*domain.Transaction) []*domain.Transaction {
	// Group CSV transactions by account (last 4 digits)
	csvByAccount := make(map[string][]*domain.Transaction)
	for _, tx := range csvTxs {
		if tx.StatementAccountNumber == nil {
			continue
		}
		last4 := GetLast4Digits(*tx.StatementAccountNumber)
		csvByAccount[last4] = append(csvByAccount[last4], tx)
	}

	// Find cutoff dates for each account
	cutoffDates := make(map[string]*time.Time)
	for last4 := range csvByAccount {
		cutoffDates[last4] = FindLatestTransactionDate(statementTxs, last4)
	}

	// Filter CSV transactions to only include those after cutoff
	var newTransactions []*domain.Transaction
	for last4, txs := range csvByAccount {
		cutoff := cutoffDates[last4]

		for _, tx := range txs {
			// Include if no cutoff date (no statement for this account) or after cutoff
			if cutoff == nil || tx.TxDate.After(*cutoff) {
				newTransactions = append(newTransactions, tx)
			}
		}
	}

	// Combine statement transactions with new CSV transactions
	result := make([]*domain.Transaction, 0, len(statementTxs)+len(newTransactions))
	result = append(result, statementTxs...)
	result = append(result, newTransactions...)

	return result
}
