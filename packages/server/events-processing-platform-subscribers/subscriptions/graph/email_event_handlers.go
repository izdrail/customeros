package graph

import (
	"context"
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/common"
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/grpc_client"
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/logger"
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/model"
	commonservice "github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/service"
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/tracing"
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/utils"
	neo4jentity "github.com/openline-ai/openline-customer-os/packages/server/customer-os-neo4j-repository/entity"
	neo4jmapper "github.com/openline-ai/openline-customer-os/packages/server/customer-os-neo4j-repository/mapper"
	neo4jrepository "github.com/openline-ai/openline-customer-os/packages/server/customer-os-neo4j-repository/repository"
	"github.com/openline-ai/openline-customer-os/packages/server/events-processing-platform-subscribers/constants"
	"github.com/openline-ai/openline-customer-os/packages/server/events-processing-platform-subscribers/service"
	"github.com/openline-ai/openline-customer-os/packages/server/events/event/email"
	"github.com/openline-ai/openline-customer-os/packages/server/events/event/email/event"
	"github.com/openline-ai/openline-customer-os/packages/server/events/eventstore"
	"github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
)

type EmailEventHandler struct {
	log         logger.Logger
	services    *service.Services
	grpcClients *grpc_client.Clients
}

func NewEmailEventHandler(log logger.Logger, services *service.Services, grpcClients *grpc_client.Clients) *EmailEventHandler {
	return &EmailEventHandler{
		log:         log,
		services:    services,
		grpcClients: grpcClients,
	}
}

func (h *EmailEventHandler) OnEmailValidatedV2(ctx context.Context, evt eventstore.Event) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "EmailEventHandler.OnEmailValidatedV2")
	defer span.Finish()
	setEventSpanTagsAndLogFields(span, evt)

	var eventData event.EmailValidatedEventV2
	if err := evt.GetJsonData(&eventData); err != nil {
		tracing.TraceErr(span, err)
		return errors.Wrap(err, "evt.GetJsonData")
	}
	span.SetTag(tracing.SpanTagTenant, eventData.Tenant)
	emailId := email.GetEmailObjectID(evt.AggregateID, eventData.Tenant)

	data := neo4jrepository.EmailValidatedFields{
		EmailAddress:      eventData.Email,
		Domain:            eventData.Domain,
		Username:          eventData.Username,
		IsValidSyntax:     eventData.IsValidSyntax,
		IsRisky:           eventData.IsRisky,
		IsFirewalled:      eventData.IsFirewalled,
		Provider:          eventData.Provider,
		Firewall:          eventData.Firewall,
		IsCatchAll:        eventData.IsCatchAll,
		Deliverable:       eventData.Deliverable,
		IsMailboxFull:     eventData.IsMailboxFull,
		IsRoleAccount:     eventData.IsRoleAccount,
		IsSystemGenerated: eventData.IsSystemGenerated,
		IsFreeAccount:     eventData.IsFreeAccount,
		SmtpSuccess:       eventData.SmtpSuccess,
		ResponseCode:      eventData.ResponseCode,
		ErrorCode:         eventData.ErrorCode,
		Description:       eventData.Description,
		ValidatedAt:       eventData.ValidatedAt,
		IsPrimaryDomain:   eventData.IsPrimaryDomain,
		PrimaryDomain:     eventData.PrimaryDomain,
		AlternateEmail:    eventData.AlternateEmail,
		RetryValidation:   eventData.RetryValidation,
	}

	err := h.services.CommonServices.Neo4jRepositories.EmailWriteRepository.EmailValidated(ctx, eventData.Tenant, emailId, data)
	if err != nil {
		tracing.TraceErr(span, errors.Wrap(err, "EmailValidated"))
	}

	contactsDbNodes, err := h.services.CommonServices.Neo4jRepositories.ContactReadRepository.GetContactsWithEmail(ctx, eventData.Tenant, eventData.Email)
	if err != nil {
		tracing.TraceErr(span, errors.Wrap(err, "GetContactsWithEmail"))
	}
	organizationDbNodes, err := h.services.CommonServices.Neo4jRepositories.OrganizationReadRepository.GetOrganizationsWithEmail(ctx, eventData.Tenant, eventData.Email)
	if err != nil {
		tracing.TraceErr(span, errors.Wrap(err, "GetOrganizationsWithEmail"))
	}

	// if alternate email is present, update the alternate email for the linked contacts and organizations
	if eventData.AlternateEmail != "" && eventData.AlternateEmail != eventData.Email {
		// add alternate email for linked contacts
		for _, contactDbNode := range contactsDbNodes {
			contactEntity := neo4jmapper.MapDbNodeToContactEntity(contactDbNode)
			innerCtx := common.WithCustomContext(ctx, &common.CustomContext{
				Tenant:    eventData.Tenant,
				AppSource: constants.AppSourceEventProcessingPlatformSubscribers,
			})
			_, err = h.services.CommonServices.EmailService.Merge(innerCtx, eventData.Tenant,
				commonservice.EmailFields{
					Email:     eventData.AlternateEmail,
					Source:    neo4jentity.DataSourceOpenline,
					AppSource: constants.AppSourceEventProcessingPlatformSubscribers,
					Primary:   true,
				},
				&commonservice.LinkWith{
					Type: model.CONTACT,
					Id:   contactEntity.Id,
				})
			if err != nil {
				tracing.TraceErr(span, errors.Wrapf(err, "Add email to contact %s", contactEntity.Id))
			}
		}

		// add alternate email for linked organizations
		for _, organizationDbNode := range organizationDbNodes {
			organizationEntity := neo4jmapper.MapDbNodeToOrganizationEntity(organizationDbNode)
			innerCtx := common.WithCustomContext(ctx, &common.CustomContext{
				Tenant:    eventData.Tenant,
				AppSource: constants.AppSourceEventProcessingPlatformSubscribers,
			})
			_, err = h.services.CommonServices.EmailService.Merge(innerCtx, eventData.Tenant,
				commonservice.EmailFields{
					Email:     eventData.AlternateEmail,
					Source:    neo4jentity.DataSourceOpenline,
					AppSource: constants.AppSourceEventProcessingPlatformSubscribers,
					Primary:   true,
				},
				&commonservice.LinkWith{
					Type: model.ORGANIZATION,
					Id:   organizationEntity.ID,
				})
			if err != nil {
				tracing.TraceErr(span, errors.Wrapf(err, "Add email to organization %s", organizationEntity.ID))
			}
		}
	}

	// notify linked contacts
	for _, contact := range contactsDbNodes {
		contactEntity := neo4jmapper.MapDbNodeToContactEntity(contact)
		utils.EventCompleted(ctx, eventData.Tenant, model.CONTACT.String(), contactEntity.Id, h.grpcClients, utils.NewEventCompletedDetails().WithUpdate())
	}
	// notify linked organizations
	for _, organization := range organizationDbNodes {
		organizationEntity := neo4jmapper.MapDbNodeToOrganizationEntity(organization)
		utils.EventCompleted(ctx, eventData.Tenant, model.ORGANIZATION.String(), organizationEntity.ID, h.grpcClients, utils.NewEventCompletedDetails().WithUpdate())
	}

	return nil
}

func (h *EmailEventHandler) OnEmailDelete(ctx context.Context, evt eventstore.Event) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "EmailEventHandler.OnEmailDelete")
	defer span.Finish()
	setEventSpanTagsAndLogFields(span, evt)

	var eventData event.EmailDeleteEvent
	if err := evt.GetJsonData(&eventData); err != nil {
		tracing.TraceErr(span, err)
		return errors.Wrap(err, "evt.GetJsonData")
	}
	emailId := email.GetEmailObjectID(evt.AggregateID, eventData.Tenant)
	tracing.TagTenant(span, eventData.Tenant)
	tracing.TagEntity(span, emailId)

	err := h.services.CommonServices.Neo4jRepositories.EmailWriteRepository.DeleteEmail(ctx, eventData.Tenant, emailId)
	if err != nil {
		tracing.TraceErr(span, err)
		h.log.Errorf("failed to delete email: %v", err)
	}

	return nil
}
