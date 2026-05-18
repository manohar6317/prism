package store

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ReviewRecord is persisted to DynamoDB after every PR review
type ReviewRecord struct {
	ID           string    `dynamodbav:"id"`           // "owner/repo#PR_number"
	Owner        string    `dynamodbav:"owner"`
	Repo         string    `dynamodbav:"repo"`
	PRNumber     int       `dynamodbav:"pr_number"`
	PRURL        string    `dynamodbav:"pr_url"`
	QualityScore int       `dynamodbav:"quality_score"`
	TotalIssues  int       `dynamodbav:"total_issues"`
	HasCritical  bool      `dynamodbav:"has_critical"`
	Summary      string    `dynamodbav:"summary"`
	ReviewedAt   time.Time `dynamodbav:"reviewed_at"`
	CommitSHA    string    `dynamodbav:"commit_sha"`
}

// DynamoStore handles review history persistence
type DynamoStore struct {
	client    *dynamodb.Client
	tableName string
}

func NewDynamoStore(client *dynamodb.Client, tableName string) *DynamoStore {
	return &DynamoStore{client: client, tableName: tableName}
}

// EnsureTable creates the reviews table if it doesn't exist
func (s *DynamoStore) EnsureTable(ctx context.Context) error {
	_, err := s.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(s.tableName),
	})
	if err == nil {
		return nil
	}

	_, err = s.client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(s.tableName),
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("owner"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("repo"), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: types.KeyTypeHash},
		},
		GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{
			{
				IndexName: aws.String("repo-index"),
				KeySchema: []types.KeySchemaElement{
					{AttributeName: aws.String("owner"), KeyType: types.KeyTypeHash},
					{AttributeName: aws.String("repo"), KeyType: types.KeyTypeRange},
				},
				Projection: &types.Projection{
					ProjectionType: types.ProjectionTypeAll,
				},
			},
		},
		BillingMode: types.BillingModePayPerRequest,
	})
	return err
}

// SaveReview persists a completed review to DynamoDB
func (s *DynamoStore) SaveReview(ctx context.Context, record *ReviewRecord) error {
	item, err := attributevalue.MarshalMap(record)
	if err != nil {
		return fmt.Errorf("failed to marshal review record: %w", err)
	}

	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item:      item,
	})
	return err
}

// GetRepoHistory returns all reviews for a given repo ordered by time
func (s *DynamoStore) GetRepoHistory(ctx context.Context, owner, repo string) ([]*ReviewRecord, error) {
	result, err := s.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		IndexName:              aws.String("repo-index"),
		KeyConditionExpression: aws.String("#o = :owner AND #r = :repo"),
		ExpressionAttributeNames: map[string]string{
			"#o": "owner",
			"#r": "repo",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":owner": &types.AttributeValueMemberS{Value: owner},
			":repo":  &types.AttributeValueMemberS{Value: repo},
		},
	})
	if err != nil {
		return nil, err
	}

	var records []*ReviewRecord
	for _, item := range result.Items {
		var record ReviewRecord
		if err := attributevalue.UnmarshalMap(item, &record); err == nil {
			records = append(records, &record)
		}
	}
	return records, nil
}
