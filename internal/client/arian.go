package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"

	"null-statement-parser/internal/domain"
	pb "null-statement-parser/internal/gen/null/v1"

	"github.com/charmbracelet/log"
	money "google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Client struct {
	conn          *grpc.ClientConn
	accountClient pb.AccountServiceClient
	txClient      pb.TransactionServiceClient
	userClient    pb.UserServiceClient
	authToken     string
	log           *log.Logger
}

func NewClient(serverURL, _, authToken string) (*Client, error) {
	var creds credentials.TransportCredentials
	if serverURL[len(serverURL)-4:] == ":443" {
		creds = credentials.NewTLS(&tls.Config{})
	} else {
		creds = insecure.NewCredentials()
	}

	conn, err := grpc.NewClient(serverURL, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server: %w", err)
	}

	return &Client{
		conn:          conn,
		accountClient: pb.NewAccountServiceClient(conn),
		txClient:      pb.NewTransactionServiceClient(conn),
		userClient:    pb.NewUserServiceClient(conn),
		authToken:     authToken,
		log:           log.NewWithOptions(os.Stderr, log.Options{Prefix: "grpc-client"}),
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) GetUser(userUUID string) (*pb.User, error) {
	ctx := c.withAuth(context.Background())
	resp, err := c.userClient.GetUser(ctx, &pb.GetUserRequest{Id: userUUID})
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	c.log.Info("successfully fetched user", "user_id", userUUID)
	return resp.User, nil
}

func (c *Client) GetAccounts(userID string) ([]*pb.Account, error) {
	ctx := c.withAuth(context.Background())
	resp, err := c.accountClient.ListAccounts(ctx, &pb.ListAccountsRequest{UserId: userID})
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}
	c.log.Info("successfully fetched accounts", "count", len(resp.Accounts))
	return resp.Accounts, nil
}

func (c *Client) CreateAccount(userID, accountName, bank string, accountType pb.AccountType, mainCurrency string) (*pb.Account, error) {
	ctx := c.withAuth(context.Background())
	resp, err := c.accountClient.CreateAccount(ctx, &pb.CreateAccountRequest{
		UserId:       userID,
		Name:         accountName,
		Bank:         bank,
		Type:         accountType,
		MainCurrency: mainCurrency,
		AnchorBalance: &money.Money{
			CurrencyCode: mainCurrency,
			Units:        0,
			Nanos:        0,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create account: %w", err)
	}
	c.log.Info("successfully created account", "account_name", accountName, "account_type", accountType, "account_id", resp.Account.Id)
	return resp.Account, nil
}

func (c *Client) FindAccountByAlias(userID, alias string) (*pb.Account, error) {
	ctx := c.withAuth(context.Background())
	resp, err := c.accountClient.FindAccountByAlias(ctx, &pb.FindAccountByAliasRequest{UserId: userID, Alias: alias})
	if err != nil {
		switch status.Code(err) {
		case codes.NotFound, codes.Unknown, codes.Internal:
			return nil, nil
		}
		return nil, err
	}
	return resp.Account, nil
}

func (c *Client) AddAccountAlias(userID string, accountID int64, alias string) error {
	ctx := c.withAuth(context.Background())
	_, err := c.accountClient.AddAccountAlias(ctx, &pb.AddAccountAliasRequest{
		UserId:    userID,
		AccountId: accountID,
		Alias:     alias,
	})
	if err != nil {
		return fmt.Errorf("failed to add account alias: %w", err)
	}
	c.log.Info("added alias to account", "account_id", accountID, "alias", alias)
	return nil
}

func (c *Client) CreateTransactionsBulk(userID string, transactions []*domain.Transaction) (int32, []error) {
	if len(transactions) == 0 {
		return 0, nil
	}

	ctx := c.withAuth(context.Background())

	inputs := make([]*pb.TransactionInput, 0, len(transactions))
	for _, tx := range transactions {
		input := &pb.TransactionInput{
			AccountId: int64(tx.AccountID),
			TxDate:    timestamppb.New(tx.TxDate),
			TxAmount: &money.Money{
				CurrencyCode: tx.TxCurrency,
				Units:        int64(tx.TxAmount),
				Nanos:        int32((tx.TxAmount - float64(int64(tx.TxAmount))) * 1e9),
			},
			Direction: c.convertDirection(tx.TxDirection),
		}
		if tx.TxDesc != "" {
			input.Description = &tx.TxDesc
		}
		if tx.Merchant != "" {
			input.Merchant = &tx.Merchant
		}
		if tx.UserNotes != "" {
			input.UserNotes = &tx.UserNotes
		}
		inputs = append(inputs, input)
	}

	resp, err := c.txClient.CreateTransaction(ctx, &pb.CreateTransactionRequest{
		UserId:       userID,
		Transactions: inputs,
	})
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			c.log.Info("skipping duplicate transactions")
			return 0, nil
		}
		return 0, []error{fmt.Errorf("failed to create transactions: %w", err)}
	}

	c.log.Info("transactions created successfully", "count", resp.CreatedCount)
	return resp.CreatedCount, nil
}

func (c *Client) withAuth(ctx context.Context) context.Context {
	md := metadata.Pairs("x-internal-key", c.authToken)
	return metadata.NewOutgoingContext(ctx, md)
}

func (c *Client) convertDirection(dir domain.Direction) pb.TransactionDirection {
	switch dir {
	case domain.In:
		return pb.TransactionDirection_DIRECTION_INCOMING
	case domain.Out:
		return pb.TransactionDirection_DIRECTION_OUTGOING
	default:
		return pb.TransactionDirection_DIRECTION_UNSPECIFIED
	}
}
