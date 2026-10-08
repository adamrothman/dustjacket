package store

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Dynamo is the DynamoDB Store over the single table.
type Dynamo struct {
	c     *dynamodb.Client
	table string
}

func NewDynamo(c *dynamodb.Client, table string) *Dynamo { return &Dynamo{c: c, table: table} }

func keyAV(k Key) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: k.PK},
		"SK": &types.AttributeValueMemberS{Value: k.SK},
	}
}

func (d *Dynamo) Get(ctx context.Context, key Key, out any) error {
	res, err := d.c.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &d.table, Key: keyAV(key), ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return err
	}
	if res.Item == nil {
		return ErrNotFound
	}
	return Raw(res.Item).Decode(out)
}

func (d *Dynamo) Put(ctx context.Context, item Item) error {
	r, err := Encode(item)
	if err != nil {
		return err
	}
	_, err = d.c.PutItem(ctx, &dynamodb.PutItemInput{TableName: &d.table, Item: r})
	return err
}

func (d *Dynamo) PutIfAbsent(ctx context.Context, item Item) error {
	r, err := Encode(item)
	if err != nil {
		return err
	}
	_, err = d.c.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &d.table, Item: r, ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if _, ok := errors.AsType[*types.ConditionalCheckFailedException](err); ok {
		return ErrConflict
	}
	return err
}

func (d *Dynamo) Delete(ctx context.Context, key Key) error {
	_, err := d.c.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.table, Key: keyAV(key), ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	if _, ok := errors.AsType[*types.ConditionalCheckFailedException](err); ok {
		return ErrNotFound
	}
	return err
}

func (d *Dynamo) Transact(ctx context.Context, ops ...Op) error {
	items := make([]types.TransactWriteItem, 0, len(ops))
	for _, op := range ops {
		key, r, err := op.encode()
		if err != nil {
			return err
		}
		var cond *string
		var names map[string]string
		var values map[string]types.AttributeValue
		switch {
		case op.IfAbsent:
			cond = aws.String("attribute_not_exists(PK)")
		case op.IfMatch != nil:
			v, err := attributevalue.Marshal(op.IfMatch.Value)
			if err != nil {
				return err
			}
			cond = aws.String("#a = :v")
			names = map[string]string{"#a": op.IfMatch.Attr}
			values = map[string]types.AttributeValue{":v": v}
		}
		switch {
		case op.Put != nil:
			items = append(items, types.TransactWriteItem{Put: &types.Put{
				TableName: &d.table, Item: r,
				ConditionExpression: cond, ExpressionAttributeNames: names, ExpressionAttributeValues: values,
			}})
		case op.Delete != nil:
			items = append(items, types.TransactWriteItem{Delete: &types.Delete{
				TableName: &d.table, Key: keyAV(key),
				ConditionExpression: cond, ExpressionAttributeNames: names, ExpressionAttributeValues: values,
			}})
		default:
			items = append(items, types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
				TableName: &d.table, Key: keyAV(key),
				ConditionExpression: cond, ExpressionAttributeNames: names, ExpressionAttributeValues: values,
			}})
		}
	}
	_, err := d.c.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	if tx, ok := errors.AsType[*types.TransactionCanceledException](err); ok {
		for _, r := range tx.CancellationReasons {
			if r.Code != nil && *r.Code == "ConditionalCheckFailed" {
				return ErrConflict
			}
		}
	}
	return err
}
