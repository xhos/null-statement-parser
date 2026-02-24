package mapping

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pb "null-statement-parser/internal/gen/null/v1"
)

// Store manages account mappings
type Store struct {
	filePath string
	Mappings map[string]string // statement account number -> arian account name
}

// NewStore creates a new mapping store
func NewStore() (*Store, error) {
	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	filePath := filepath.Join(cwd, "account-mappings.txt")

	store := &Store{
		filePath: filePath,
		Mappings: make(map[string]string),
	}

	// Load existing mappings if file exists
	if _, err := os.Stat(filePath); err == nil {
		if err := store.Load(); err != nil {
			return nil, err
		}
	}

	return store, nil
}

// Load reads mappings from disk
func (s *Store) Load() error {
	file, err := os.Open(s.filePath)
	if err != nil {
		return fmt.Errorf("failed to open mappings file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue // Skip empty lines and comments
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue // Skip invalid lines
		}

		statementAccount := strings.TrimSpace(parts[0])
		arianAccount := strings.TrimSpace(parts[1])
		s.Mappings[statementAccount] = arianAccount
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read mappings: %w", err)
	}

	return nil
}

// Save writes mappings to disk
func (s *Store) Save() error {
	file, err := os.Create(s.filePath)
	if err != nil {
		return fmt.Errorf("failed to create mappings file: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Write header comment
	_, err = writer.WriteString("# Account mappings: statement_account -> arian_account\n")
	if err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}

	// Write mappings in sorted order for consistency
	for statementAccount, arianAccount := range s.Mappings {
		_, err = writer.WriteString(fmt.Sprintf("%s: %s\n", statementAccount, arianAccount))
		if err != nil {
			return fmt.Errorf("failed to write mapping: %w", err)
		}
	}

	return nil
}

// FindMapping looks up an existing mapping
func (s *Store) FindMapping(statementAccountNumber string) string {
	return s.Mappings[statementAccountNumber]
}

// AddMapping adds a new mapping
func (s *Store) AddMapping(statementAccountNumber, arianAccountName string) error {
	s.Mappings[statementAccountNumber] = arianAccountName
	return s.Save()
}

// ResolveAccount finds an account by name from a list of accounts
func (s *Store) ResolveAccount(arianAccountName string, accounts []*pb.Account) *pb.Account {
	if arianAccountName == "" {
		return nil
	}

	for _, account := range accounts {
		if strings.EqualFold(account.Name, arianAccountName) {
			return account
		}
	}

	return nil
}
