package repository

import (
	"context"
	"fmt"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/db"
	"github.com/openline-ai/openline-customer-os/packages/runner/sync-gmail/entity"
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/utils"
	"time"
)

type InteractionEventRepository interface {
	GetInteractionEventIdByExternalId(ctx context.Context, tenant, externalSystemId, externalId string) (string, error)

	MergeInteractionSession(ctx context.Context, tx neo4j.ManagedTransaction, tenant, identifier string, syncDate time.Time, message entity.EmailMessageData, source, appSource string) (string, error)
	MergeEmailInteractionEvent(ctx context.Context, tx neo4j.ManagedTransaction, tenant string, syncDate time.Time, message entity.EmailMessageData, source, appSource string) (string, error)
	LinkInteractionEventToSession(ctx context.Context, tx neo4j.ManagedTransaction, tenant, interactionEventId, interactionSessionId string) error

	InteractionEventSentByEmail(ctx context.Context, tx neo4j.ManagedTransaction, tenant, interactionEventId, emailId string) error
	InteractionEventSentToEmails(ctx context.Context, tx neo4j.ManagedTransaction, tenant, interactionEventId, sentType string, emailsId []string) error
}

type interactionEventRepository struct {
	driver *neo4j.DriverWithContext
}

func NewInteractionEventRepository(driver *neo4j.DriverWithContext) InteractionEventRepository {
	return &interactionEventRepository{
		driver: driver,
	}
}

func (r *interactionEventRepository) GetInteractionEventIdByExternalId(ctx context.Context, tenant, externalSystemId, externalId string) (string, error) {
	session := utils.NewNeo4jReadSession(ctx, *r.driver)
	defer session.Close(ctx)

	query := "MATCH (ie:InteractionEvent_%s)-[IS_LINKED_WITH{externalId:$externalId}]-(e:ExternalSystem{id:$externalSystemId})" +
		" RETURN ie.id"

	dbRecord, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		queryResult, err := tx.Run(ctx, fmt.Sprintf(query, tenant),
			map[string]interface{}{
				"externalId":       externalId,
				"externalSystemId": externalSystemId,
			})
		if err != nil {
			return nil, err
		}
		record, err := queryResult.Single(ctx)
		if err != nil {
			return nil, err
		}
		return record, nil
	})
	if err != nil && err.Error() == "Result contains no more records" {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return dbRecord.(*db.Record).Values[0].(string), nil
}

func (r *interactionEventRepository) MergeInteractionSession(ctx context.Context, tx neo4j.ManagedTransaction, tenant, identifier string, syncDate time.Time, message entity.EmailMessageData, source, appSource string) (string, error) {
	cypher := ""
	if identifier == "" {
		cypher += `MATCH (:Tenant {name:$tenant}) 
			 	CREATE (is:InteractionSession_%s {identifier:$identifier, channel:$channel})
				SET `
	} else {
		cypher += `MATCH (:Tenant {name:$tenant}) 
			 	MERGE (is:InteractionSession_%s {identifier:$identifier, channel:$channel}) 
			 	ON CREATE SET `
	}

	cypher += ` is:InteractionSession,
			is.id=randomUUID(),
			is.syncDate=$syncDate,
			is.createdAt=$createdAt,
			is.name=$name,
			is.status=$status,
			is.type=$type,
			is.sourceOfTruth=$sourceOfTruth,
			is.appSource=$appSource
		WITH is
		RETURN is.id`

	queryResult, err := tx.Run(ctx, fmt.Sprintf(cypher, tenant),
		map[string]interface{}{
			"tenant":        tenant,
			"source":        source,
			"sourceOfTruth": "openline",
			"appSource":     appSource,
			"identifier":    identifier,
			"name":          message.Subject,
			"syncDate":      syncDate,
			"createdAt":     message.CreatedAt,
			"status":        "ACTIVE",
			"type":          "THREAD",
			"channel":       "EMAIL",
		})
	if err != nil {
		return "", err
	}
	record, err := queryResult.Single(ctx)
	if err != nil {
		return "", err
	}
	return record.Values[0].(string), nil
}

func (r *interactionEventRepository) MergeEmailInteractionEvent(ctx context.Context, tx neo4j.ManagedTransaction, tenant string, syncDate time.Time, message entity.EmailMessageData, source, appSource string) (string, error) {
	query := "MATCH (:Tenant {name:$tenant})<-[:EXTERNAL_SYSTEM_BELONGS_TO_TENANT]-(e:ExternalSystem {id:$externalSystemId}) " +
		" MERGE (ie:InteractionEvent_%s {source:$source, channel:$channel})-[rel:IS_LINKED_WITH {externalId:$externalId}]->(e) " +
		" ON CREATE SET " +
		"  ie:InteractionEvent, " +
		"  ie:TimelineEvent, " +
		"  ie:TimelineEvent_%s, " +
		"  rel.syncDate=$syncDate, " +
		"  ie.createdAt=$createdAt, " +
		"  ie.id=randomUUID(), " +
		"  ie.identifier=$identifier, " +
		"  ie.channel=$channel, " +
		"  ie.channelData=$channelData, " +
		"  ie.content=$content, " +
		"  ie.contentType=$contentType, " +
		"  ie.sourceOfTruth=$sourceOfTruth, " +
		"  ie.appSource=$appSource " +
		" WITH ie " +
		" RETURN ie.id"

	params := map[string]interface{}{
		"tenant":           tenant,
		"identifier":       message.ExternalId,
		"source":           source,
		"sourceOfTruth":    "openline",
		"appSource":        appSource,
		"externalId":       message.ExternalId,
		"externalSystemId": message.ExternalSystem,
		"syncDate":         syncDate,
		"createdAt":        message.CreatedAt,
		"channel":          message.Channel,
		"channelData":      message.ChannelData,
	}

	if message.Html != "" {
		params["content"] = message.Html
		params["contentType"] = "text/html"
	} else {
		params["content"] = message.Text
		params["contentType"] = "text/plain"
	}

	queryResult, err := tx.Run(ctx, fmt.Sprintf(query, tenant, tenant),
		params)
	if err != nil {
		return "", err
	}
	record, err := queryResult.Single(ctx)
	if err != nil {
		return "", err
	}

	return record.Values[0].(string), nil
}

func (r *interactionEventRepository) LinkInteractionEventToSession(ctx context.Context, tx neo4j.ManagedTransaction, tenant, interactionEventId, interactionSessionId string) error {
	query := "MATCH (is:InteractionSession_%s {id:$interactionSessionId}) " +
		" MATCH (ie:InteractionEvent {id:$interactionEventId})" +
		" MERGE (ie)-[:PART_OF]->(is) "
	_, err := tx.Run(ctx, fmt.Sprintf(query, tenant),
		map[string]interface{}{
			"tenant":               tenant,
			"interactionSessionId": interactionSessionId,
			"interactionEventId":   interactionEventId,
		})
	return err
}

func (r *interactionEventRepository) InteractionEventSentByEmail(ctx context.Context, tx neo4j.ManagedTransaction, tenant, interactionEventId, emailId string) error {
	query := "MATCH (is:InteractionEvent_%s {id:$interactionEventId}) " +
		" MATCH (e:Email_%s {id: $emailId}) " +
		" MERGE (is)-[:SENT_BY]->(e) "
	_, err := tx.Run(ctx, fmt.Sprintf(query, tenant, tenant),
		map[string]interface{}{
			"tenant":             tenant,
			"interactionEventId": interactionEventId,
			"emailId":            emailId,
		})
	return err
}

func (r *interactionEventRepository) InteractionEventSentToEmails(ctx context.Context, tx neo4j.ManagedTransaction, tenant, interactionEventId, sentType string, emailsId []string) error {
	query := "MATCH (ie:InteractionEvent_%s {id:$interactionEventId}) " +
		" MATCH (e:Email_%s) WHERE e.id in $emailsId " +
		" MERGE (ie)-[:SENT_TO {type: $sentType}]->(e) "
	_, err := tx.Run(ctx, fmt.Sprintf(query, tenant, tenant),
		map[string]interface{}{
			"tenant":             tenant,
			"interactionEventId": interactionEventId,
			"sentType":           sentType,
			"emailsId":           emailsId,
		})
	return err
}
