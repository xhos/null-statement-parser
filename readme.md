# arian-statement-parser

A wrapper around [andrewscwei's rbc-statement-parser](https://github.com/andrewscwei/rbc-statement-parser) that parses RBC PDF statements and uploads transactions directly to [ariand](https://github.com/xhos/ariand).

## Setup

1. Copy the environment file and configure it:

```bash
cp .env.example .env
```

2. Set your environment variables in `.env`:

```env
ARIAND_URL=api.arian.xhos.dev:443
API_KEY=your_api_key_here
USER_ID=your_user_id_here
```

3. Install dependencies:

```bash
go mod tidy
cd rbc-statement-parser && uv sync
```

## Usage

### Parse PDF Statements

```bash
go run cmd/main.go -pdf <path-to-pdf-folder>
```

### Parse CSV Export

RBC also provides CSV exports for recent transactions (last few months). You can parse these in addition to or instead of PDF statements:

```bash
# Parse CSV only
go run cmd/main.go -csv <path-to-csv-file>

# Parse PDFs and merge with CSV (smart deduplication)
go run cmd/main.go -pdf <path-to-pdf-folder> -csv <path-to-csv-file>
```

When both PDF and CSV are provided, the parser will:
1. Parse all PDF statements first
2. Parse the CSV file
3. **Smart merge**: For each account, find the latest transaction date in the PDF statements
4. Only add CSV transactions that are **after** that cutoff date
5. Avoid duplicates automatically

This allows you to use PDF statements for historical data and CSV for the gap between the latest statement and today.

The parser will:

1. Parse all PDF statements in the specified folder (if `-pdf` provided)
2. Parse CSV file and merge with smart deduplication (if `-csv` provided)
3. Display a summary of processed files and transactions
4. Ask for confirmation before uploading to Arian
5. Create accounts automatically if they don't exist
6. Upload all transactions to your Arian account

### Command-line Options

- `-pdf`: Path to folder containing PDF statements (optional if `-csv` provided)
- `-csv`: Path to RBC CSV export file (optional)
- `-config`: Path to Python parser config file (optional)

All other configuration (USER_ID, ARIAND_URL, API_KEY) is done via environment variables.

## File Naming

**Filenames don't matter!** The parser is completely filename-independent. It automatically extracts all account information directly from the PDF content:

- **Account type** (chequing, savings, or visa)
- **Account number** (e.g., `05172-5163878` for chequing/savings, `3802` for VISA)
- **Account name** (e.g., `RBC Advantage Banking`, `RBC High Interest eSavings`)

You can name your PDF files anything you want - `statement.pdf`, `2024-01.pdf`, `random-name.pdf` - it doesn't matter. The parser reads the actual PDF header to determine everything.

For your own organization, you might want to include dates or account identifiers in filenames, but it's entirely optional.

## Account Matching & Creation

The parser automatically matches and creates accounts based on PDF-extracted data:

1. **Matching**: Tries to match by account number and type (e.g., account number `05172-5163878` with type `savings`)
2. **Creation**: If no match is found, creates a new account using:
   - **Name**: Extracted from PDF (e.g., `RBC Advantage Banking`, `RBC High Interest eSavings`, `VISA`)
   - **Number**: Full account number or last 4 digits for VISA
   - **Type**: Automatically detected (chequing, savings, or credit card)
   - **Bank**: RBC (hardcoded for now)

All account information comes from the PDF content, not from filenames.

## CSV Format

The parser expects the standard RBC CSV export format with the following columns:

```csv
"Account Type","Account Number","Transaction Date","Cheque Number","Description 1","Description 2","CAD$","USD$"
```

Key features:
- **Account matching**: The parser matches CSV accounts to statement accounts using the last 4 digits
  - CSV has full account numbers (e.g., `4510154225745546`)
  - Statements show last 4 digits (e.g., `5546`)
  - Matching is automatic based on these digits
- **Account types**: Supports Chequing, Savings, and Visa
- **Date format**: M/D/YYYY or MM/DD/YYYY
- **Amounts**: Supports both CAD$ and USD$ columns
- **Smart deduplication**: When merged with PDF statements, only includes transactions after the latest statement date per account

## Requirements

- Go 1.21+
- Python 3 with uv (for running the RBC statement parser)
- Access to Arian gRPC server with valid API credentials
