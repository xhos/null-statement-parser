# arian-statement-parser

Wraps [andrewscwei's rbc-statement-parser](https://github.com/andrewscwei/rbc-statement-parser) to parse RBC PDF statements and upload transactions to [ariand](https://github.com/xhos/ariand).

## Setup

```bash
cp .env.example .env
# fill in NULL_CORE_URL, API_KEY, USER_ID

go mod tidy
cd rbc-statement-parser && uv sync
```

## Usage

```bash
# PDF only
go run cmd/main.go -pdf <folder>

# CSV only
go run cmd/main.go -csv <file>

# both (CSV fills the gap between latest statement and today)
go run cmd/main.go -pdf <folder> -csv <file>
```

Flags: `-pdf`, `-csv`, `-config` (python parser config, optional)

On first run, unknown statement accounts are prompted — pick an existing Arian account or create one. The account number is registered as an alias so subsequent runs skip the prompt.

## Notes

- Filenames don't matter, everything is read from PDF content
- CSV deduplication: only transactions after the latest PDF statement date per account are included
- CSV format: standard RBC export (`Account Type, Account Number, Transaction Date, ...`)
- CSV account numbers are matched to statements by last 4 digits
