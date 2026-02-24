package parser

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"null-statement-parser/internal/domain"
)

type PythonTransaction struct {
	Date          string  `json:"date"`
	Amount        float64 `json:"amount"`
	Method        string  `json:"method"`
	Category      string  `json:"category"`
	Code          *string `json:"code"`
	Description   string  `json:"description"`
	PostingDate   string  `json:"posting_date"`
	AccountNumber *string `json:"account_number"`
	AccountType   string  `json:"account_type"`
	AccountName   string  `json:"account_name"`
	SourceFile    string  `json:"source_file"`
}

type FileResult struct {
	File             string `json:"file"`
	TransactionCount int    `json:"transaction_count"`
	Processed        bool   `json:"processed"`
}

type ParseResult struct {
	Transactions []PythonTransaction `json:"transactions"`
	FileResults  []FileResult        `json:"file_results"`
	Summary      struct {
		TotalFiles        int `json:"total_files"`
		ProcessedFiles    int `json:"processed_files"`
		TotalTransactions int `json:"total_transactions"`
	} `json:"summary"`
}

type PythonParser struct {
	pythonPath string
	scriptPath string
}

func NewPythonParser() *PythonParser {
	return &PythonParser{
		pythonPath: "uv",
		scriptPath: "rbc-statement-parser/main.py",
	}
}

func (p *PythonParser) ParseStatements(pdfPath string, configPath string) (*ParseResult, []*domain.Transaction, error) {
	// Build command args with JSON format
	// Only prepend ../ if the path is relative
	pythonPdfPath := pdfPath
	if !filepath.IsAbs(pdfPath) {
		pythonPdfPath = "../" + pdfPath
	}

	args := []string{"run", "python", "main.py", pythonPdfPath, "--format", "json"}
	if configPath != "" {
		pythonConfigPath := configPath
		if !filepath.IsAbs(configPath) {
			pythonConfigPath = "../" + configPath
		}
		args = append(args, "--config", pythonConfigPath)
	}

	// Execute Python script with uv from the rbc-statement-parser directory
	cmd := exec.Command(p.pythonPath, args...)
	cmd.Dir = "rbc-statement-parser" // Set working directory
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to execute Python parser: %w\nOutput: %s", err, string(output))
	}

	// Parse JSON output
	return p.parseJSONOutput(string(output))
}

func (p *PythonParser) parseJSONOutput(output string) (*ParseResult, []*domain.Transaction, error) {
	var result ParseResult

	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, nil, fmt.Errorf("failed to parse JSON output: %w", err)
	}

	var transactions []*domain.Transaction

	for _, pt := range result.Transactions {
		// Parse date
		txDate, err := time.Parse("2006-01-02T15:04:05", pt.Date)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse date %s: %w", pt.Date, err)
		}

		// Determine direction and make amount positive
		var direction domain.Direction
		amount := pt.Amount
		if amount < 0 {
			direction = domain.Out
			amount = -amount
		} else {
			direction = domain.In
		}

		tx := &domain.Transaction{
			TxDate:                 txDate,
			TxAmount:               amount,
			TxCurrency:             "CAD", // Default to CAD for RBC statements
			TxDirection:            direction,
			TxDesc:                 pt.Description,
			StatementAccountNumber: pt.AccountNumber,
			StatementAccountType:   pt.AccountType,
			StatementAccountName:   pt.AccountName,
			SourceFilePath:         pt.SourceFile,
		}

		transactions = append(transactions, tx)
	}

	return &result, transactions, nil
}
