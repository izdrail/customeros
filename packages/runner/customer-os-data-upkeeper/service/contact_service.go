package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/customeros/mailsherpa/emailparser"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/openline-ai/openline-customer-os/packages/runner/customer-os-data-upkeeper/config"
	"github.com/openline-ai/openline-customer-os/packages/runner/customer-os-data-upkeeper/constants"
	"github.com/openline-ai/openline-customer-os/packages/runner/customer-os-data-upkeeper/logger"
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/common"
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/dto"
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/model"
	commonService "github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/service"
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/service/security"
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/tracing"
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/utils"
	neo4jentity "github.com/openline-ai/openline-customer-os/packages/server/customer-os-neo4j-repository/entity"
	neo4jmapper "github.com/openline-ai/openline-customer-os/packages/server/customer-os-neo4j-repository/mapper"
	neo4jmodel "github.com/openline-ai/openline-customer-os/packages/server/customer-os-neo4j-repository/model"
	neo4jrepository "github.com/openline-ai/openline-customer-os/packages/server/customer-os-neo4j-repository/repository"
	postgresentity "github.com/openline-ai/openline-customer-os/packages/server/customer-os-postgres-repository/entity"
	postgresrepository "github.com/openline-ai/openline-customer-os/packages/server/customer-os-postgres-repository/repository"
	enrichmentmodel "github.com/openline-ai/openline-customer-os/packages/server/enrichment-api/model"
	commonpb "github.com/openline-ai/openline-customer-os/packages/server/events-processing-proto/gen/proto/go/api/grpc/v1/common"
	contactpb "github.com/openline-ai/openline-customer-os/packages/server/events-processing-proto/gen/proto/go/api/grpc/v1/contact"
	phonenumberpb "github.com/openline-ai/openline-customer-os/packages/server/events-processing-proto/gen/proto/go/api/grpc/v1/phone_number"
	"github.com/openline-ai/openline-customer-os/packages/server/events/eventbuffer"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
	"github.com/pkg/errors"
	"io"
	"net/http"
)

type ContactService interface {
	UpkeepContacts()
	AskForWorkEmailOnBetterContact()
	EnrichWithWorkEmailFromBetterContact()
	CheckBetterContactRequestsWithoutResponse()
	EnrichContacts()
	AskForLinkedInConnections()
	ProcessLinkedInConnections()
	LinkOrphanContactsToOrganizationBaseOnLinkedinScrapIn()
}

type contactService struct {
	cfg                *config.Config
	log                logger.Logger
	commonServices     *commonService.Services
	eventBufferService *eventbuffer.EventBufferStoreService
}

func NewContactService(cfg *config.Config, log logger.Logger, commonServices *commonService.Services, eventBufferService *eventbuffer.EventBufferStoreService) ContactService {
	return &contactService{
		cfg:                cfg,
		log:                log,
		commonServices:     commonServices,
		eventBufferService: eventBufferService,
	}
}

func (s *contactService) UpkeepContacts() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Cancel context on exit

	s.removeEmptySocials(ctx)
	s.removeDuplicatedSocials(ctx)
	s.hideContactsWithGroupOrSystemGeneratedEmail(ctx)
	s.checkContacts(ctx)
	s.updateContactNamesFromEmails(ctx)
}

func (s *contactService) removeEmptySocials(ctx context.Context) {
	span, ctx := tracing.StartTracerSpan(ctx, "ContactService.removeEmptySocials")
	defer span.Finish()
	tracing.TagComponentCronJob(span)

	limit := 100

	for {
		select {
		case <-ctx.Done():
			s.log.Infof("Context cancelled, stopping")
			return
		default:
			// continue as normal
		}

		minutesSinceLastUpdate := 180
		records, err := s.commonServices.Neo4jRepositories.SocialReadRepository.GetEmptySocialsForEntityType(ctx, model.NodeLabelContact, minutesSinceLastUpdate, limit)
		if err != nil {
			tracing.TraceErr(span, err)
			s.log.Errorf("Error getting socials: %v", err)
			return
		}

		// no record
		if len(records) == 0 {
			return
		}

		//remove socials from contact
		for _, record := range records {
			err := s.commonServices.Neo4jRepositories.SocialWriteRepository.RemoveSocialForEntityById(ctx, record.Tenant, record.LinkedEntityId, model.NodeLabelContact, record.SocialId)
			if err != nil {
				tracing.TraceErr(span, err)
				s.log.Errorf("Error removing social {%s}: %s", record.SocialId, err.Error())
			}
		}

		// if less than limit records are returned, we are done
		if len(records) < limit {
			return
		}

		// force exit after single iteration
		return
	}
}

func (s *contactService) removeDuplicatedSocials(ctx context.Context) {
	span, ctx := tracing.StartTracerSpan(ctx, "ContactService.removeDuplicatedSocials")
	defer span.Finish()
	tracing.TagComponentCronJob(span)

	limit := 100

	for {
		select {
		case <-ctx.Done():
			s.log.Infof("Context cancelled, stopping")
			return
		default:
			// continue as normal
		}

		minutesSinceLastUpdate := 180
		records, err := s.commonServices.Neo4jRepositories.SocialReadRepository.GetDuplicatedSocialsForEntityType(ctx, model.NodeLabelContact, minutesSinceLastUpdate, limit)
		if err != nil {
			tracing.TraceErr(span, err)
			s.log.Errorf("Error getting socials: %v", err)
			return
		}

		// no record
		if len(records) == 0 {
			return
		}

		//remove socials from contact
		for _, record := range records {
			err := s.commonServices.Neo4jRepositories.SocialWriteRepository.RemoveSocialForEntityById(ctx, record.Tenant, record.LinkedEntityId, model.NodeLabelContact, record.SocialId)
			if err != nil {
				tracing.TraceErr(span, err)
				s.log.Errorf("Error removing social {%s}: %s", record.SocialId, err.Error())
			}
			if err != nil {
				tracing.TraceErr(span, err)
				s.log.Errorf("Error removing social {%s}: %s", record.SocialId, err.Error())
			}
		}

		// if less than limit records are returned, we are done
		if len(records) < limit {
			return
		}

		// force exit after single iteration
		return
	}
}

func (s *contactService) hideContactsWithGroupOrSystemGeneratedEmail(ctx context.Context) {
	span, ctx := tracing.StartTracerSpan(ctx, "ContactService.hideContactsWithGroupOrSystemGeneratedEmail")
	defer span.Finish()
	tracing.TagComponentCronJob(span)

	limit := 100

	for {
		select {
		case <-ctx.Done():
			s.log.Infof("Context cancelled, stopping")
			return
		default:
			// continue as normal
		}

		records, err := s.commonServices.Neo4jRepositories.ContactReadRepository.GetContactsWithGroupOrSystemGeneratedEmail(ctx, limit)
		if err != nil {
			tracing.TraceErr(span, err)
			s.log.Errorf("Error getting contacts: %v", err)
			return
		}

		// no record
		if len(records) == 0 {
			return
		}

		//hide contact
		for _, record := range records {
			// create new context from main one with custom context
			innerCtx := common.WithCustomContext(ctx, &common.CustomContext{
				Tenant:    record.Tenant,
				AppSource: constants.AppSourceDataUpkeeper,
			})

			err = s.commonServices.ContactService.HideContact(innerCtx, record.ContactId)
			if err != nil {
				tracing.TraceErr(span, err)
				s.log.Errorf("Error hiding contact {%s}: %s", record.ContactId, err.Error())
			}
		}

		// if less than limit records are returned, we are done
		if len(records) < limit {
			return
		}

		// force exit after single iteration
		return
	}
}

func (s *contactService) checkContacts(ctx context.Context) {
	span, ctx := tracing.StartTracerSpan(ctx, "ContactService.checkContacts")
	defer span.Finish()
	tracing.TagComponentCronJob(span)

	limit := 1000
	minutesSinceLastUpdate := 180
	hoursSinceLastCheck := 24

	for {
		select {
		case <-ctx.Done():
			s.log.Infof("Context cancelled, stopping")
			return
		default:
			// continue as normal
		}

		records, err := s.commonServices.Neo4jRepositories.ContactReadRepository.GetContactsToCheck(ctx, minutesSinceLastUpdate, hoursSinceLastCheck, limit)
		if err != nil {
			tracing.TraceErr(span, err)
			s.log.Errorf("Error getting contacts: %v", err)
			return
		}

		// no record
		if len(records) == 0 {
			return
		}

		// update contact names
		for _, record := range records {
			// create new context from main one with custom context
			innerCtx := common.WithCustomContext(ctx, &common.CustomContext{
				Tenant:    record.Tenant,
				AppSource: constants.AppSourceDataUpkeeper,
			})

			contactEntity := neo4jmapper.MapDbNodeToContactEntity(record.Contact)
			cleanFirstName := utils.CleanName(contactEntity.FirstName)
			cleanLastName := utils.CleanName(contactEntity.LastName)
			cleanName := utils.CleanName(contactEntity.Name)

			saveContact := false
			contactFields := neo4jrepository.ContactFields{}
			if cleanFirstName != contactEntity.FirstName {
				contactFields.FirstName = cleanFirstName
				contactFields.UpdateFirstName = true
				saveContact = true
			}
			if cleanLastName != contactEntity.LastName {
				contactFields.LastName = cleanLastName
				contactFields.UpdateLastName = true
				saveContact = true
			}
			if cleanName != contactEntity.Name {
				contactFields.Name = cleanName
				contactFields.UpdateName = true
				saveContact = true
			}

			if saveContact {
				_, err = s.commonServices.ContactService.SaveContact(innerCtx, &contactEntity.Id, contactFields, "", neo4jmodel.ExternalSystem{})
				if err != nil {
					tracing.TraceErr(span, errors.Wrap(err, "ContactService.SaveContact"))
					s.log.Errorf("Error updating contact {%s}: %s", contactEntity.Id, err.Error())
				}
			}

			// mark contact as checked
			err = s.commonServices.Neo4jRepositories.CommonWriteRepository.UpdateTimeProperty(innerCtx, record.Tenant, model.NodeLabelContact, contactEntity.Id, string(neo4jentity.ContactPropertyCheckedAt), utils.NowPtr())
			if err != nil {
				tracing.TraceErr(span, err)
				s.log.Errorf("Error updating contact' checked at: %s", err.Error())
			}
		}

		// if less than limit records are returned, we are done
		if len(records) < limit {
			return
		}

		// force exit after single iteration
		return
	}
}

func (s *contactService) updateContactNamesFromEmails(ctx context.Context) {
	span, ctx := tracing.StartTracerSpan(ctx, "ContactService.updateContactNamesFromEmails")
	defer span.Finish()
	tracing.TagComponentCronJob(span)

	limit := 100

	for {
		select {
		case <-ctx.Done():
			s.log.Infof("Context cancelled, stopping")
			return
		default:
			// continue as normal
		}

		records, err := s.commonServices.Neo4jRepositories.ContactReadRepository.GetContactsWithEmailForNameUpdate(ctx, limit)
		if err != nil {
			tracing.TraceErr(span, err)
			s.log.Errorf("Error getting contacts: %v", err)
			return
		}

		// no record
		if len(records) == 0 {
			return
		}

		// update contact names
		for _, record := range records {
			// create new context from main one with custom context
			innerCtx := common.WithCustomContext(ctx, &common.CustomContext{
				Tenant:    record.Tenant,
				AppSource: constants.AppSourceDataUpkeeper,
			})

			parsedEmail, err := emailparser.Parse(record.FieldStr1)
			if err != nil {
				tracing.TraceErr(span, err)
				s.log.Errorf("Error parsing email {%s}: %s", record.FieldStr1, err.Error())
				continue
			}

			saveContact := false
			contactFields := neo4jrepository.ContactFields{}
			if parsedEmail.FirstName != "" {
				contactFields.FirstName = utils.CleanName(parsedEmail.FirstName)
				contactFields.UpdateFirstName = true
				saveContact = true
			}
			if parsedEmail.LastName != "" {
				contactFields.LastName = utils.CleanName(parsedEmail.LastName)
				contactFields.UpdateLastName = true
				saveContact = true
			}
			if saveContact {
				_, err = s.commonServices.ContactService.SaveContact(innerCtx, &record.ContactId, contactFields, "", neo4jmodel.ExternalSystem{})
				if err != nil {
					tracing.TraceErr(span, errors.Wrap(err, "ContactService.SaveContact"))
					s.log.Errorf("Error updating contact {%s}: %s", record.ContactId, err.Error())
				}
			}
		}

		// if less than limit records are returned, we are done
		if len(records) < limit {
			return
		}

		// force exit after single iteration
		return
	}
}

func (s *contactService) AskForWorkEmailOnBetterContact() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Cancel context on exit

	s.findEmailsWithBetterContact(ctx)
}

func (s *contactService) CheckBetterContactRequestsWithoutResponse() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Cancel context on exit

	s.checkBetterContactRequestsWithoutResponse(ctx)
}

func (s *contactService) AskForLinkedInConnections() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.askForLinkedInConnections(ctx)
}

func (s *contactService) LinkOrphanContactsToOrganizationBaseOnLinkedinScrapIn() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.linkOrphanContactsToOrganizationBaseOnLinkedinScrapIn(ctx)
}

type BetterContactRequestBody struct {
	Data    []BetterContactData `json:"data"`
	Webhook string              `json:"webhook"`
}

type BetterContactData struct {
	FirstName     string `json:"first_name"`
	LastName      string `json:"last_name"`
	LinkedInUrl   string `json:"linkedin_url"`
	Company       string `json:"company"`
	CompanyDomain string `json:"company_domain"`
}

type BetterContactResponseBody struct {
	Success bool   `json:"success"`
	ID      string `json:"id"`
	Message string `json:"message"`
}

func (s *contactService) askForLinkedInConnections(c context.Context) {
	span, ctx := tracing.StartTracerSpan(c, "ContactService.askForLinkedInConnections")
	defer span.Finish()
	tracing.TagComponentCronJob(span)

	linkedinTokens, err := s.commonServices.PostgresRepositories.BrowserConfigRepository.Get(ctx)
	if err != nil {
		tracing.TraceErr(span, err)
		return
	}

	span.LogFields(log.Int("linkedinTokens", len(linkedinTokens)))

	for _, linkedinToken := range linkedinTokens {
		//todo check if there is already a scheduled job for this token today
		err := s.commonServices.PostgresRepositories.BrowserAutomationRunRepository.Add(ctx, &postgresentity.BrowserAutomationsRun{
			BrowserConfigId: linkedinToken.Id,
			UserId:          linkedinToken.UserId,
			Tenant:          linkedinToken.Tenant,
			Type:            "FIND_CONNECTIONS",
			Status:          "SCHEDULED",
			Payload:         "\"\"",
		})
		if err != nil {
			tracing.TraceErr(span, err)
			break
		}
	}

}

func (s *contactService) ProcessLinkedInConnections() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	span, ctx := tracing.StartTracerSpan(ctx, "ContactService.ProcessLinkedInConnections")
	defer span.Finish()
	tracing.TagComponentCronJob(span)

	automationsRuns, err := s.commonServices.PostgresRepositories.BrowserAutomationRunRepository.Get(ctx, "FIND_CONNECTIONS", "COMPLETED")
	if err != nil {
		tracing.TraceErr(span, err)
		return
	}

	span.LogFields(log.Int("processing", len(automationsRuns)))

	for _, automationRun := range automationsRuns {
		ctx = common.WithCustomContext(ctx, &common.CustomContext{
			Tenant:    automationRun.Tenant,
			AppSource: constants.AppSourceDataUpkeeper,
		})
		s.processAutomationRunResult(ctx, automationRun)
	}
}

func (s *contactService) processAutomationRunResult(ctx context.Context, automationRun postgresentity.BrowserAutomationsRun) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "ContactService.processAutomationRunResult")
	defer span.Finish()
	tracing.TagComponentCronJob(span)

	result, err := s.commonServices.PostgresRepositories.BrowserAutomationRunResultRepository.Get(ctx, automationRun.Id)
	if err != nil {
		tracing.TraceErr(span, err)
		return
	}

	if result == nil || result.ResultData == "" {
		span.LogFields(log.String("results", "empty"))
		return
	}

	useByEmailNode, err := s.commonServices.Neo4jRepositories.UserReadRepository.GetUserById(ctx, automationRun.Tenant, automationRun.UserId)
	if err != nil {
		tracing.TraceErr(span, errors.Wrap(err, "UserReadRepository.GetUserById"))
		return
	}
	if useByEmailNode == nil {
		tracing.TraceErr(span, errors.Wrap(err, "User does not exist"))
		return
	}

	var results []string

	err = json.Unmarshal([]byte(result.ResultData), &results)
	if err != nil {
		tracing.TraceErr(span, err)
		return
	}

	span.LogFields(log.Int("results", len(results)))

	tenant := automationRun.Tenant
	userId := automationRun.UserId

	for _, linkedinUrl := range results {
		err := s.processLinkedInUrl(ctx, tenant, linkedinUrl, userId)
		if err != nil {
			tracing.TraceErr(span, err)
			return
		}

	}

	err = s.commonServices.PostgresRepositories.BrowserAutomationRunRepository.MarkAsProcessed(ctx, automationRun.Id)
	if err != nil {
		tracing.TraceErr(span, err)
		return
	}
}

func (s *contactService) processLinkedInUrl(ctx context.Context, tenant, linkedinUrl, userId string) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "ContactService.processLinkedInUrl")
	defer span.Finish()
	tracing.TagComponentCronJob(span)

	linkedinProfileUrl := linkedinUrl
	if linkedinProfileUrl != "" && linkedinProfileUrl[len(linkedinProfileUrl)-1] != '/' {
		linkedinProfileUrl = linkedinProfileUrl + "/"
	}

	linkedinProfileUrl = utils.NormalizeString(linkedinProfileUrl)

	contactsWithLinkedin, err := s.commonServices.Neo4jRepositories.ContactReadRepository.GetContactsWithSocialUrl(ctx, tenant, linkedinProfileUrl)
	if err != nil {
		tracing.TraceErr(span, errors.Wrap(err, "ContactReadRepository.GetContactsWithSocialUrl"))
		return err
	}

	var contactIds []string
	if len(contactsWithLinkedin) == 0 {
		contactId, err := s.commonServices.ContactService.SaveContact(ctx, nil, neo4jrepository.ContactFields{}, linkedinProfileUrl, neo4jmodel.ExternalSystem{})
		if err != nil {
			tracing.TraceErr(span, err)
			return err
		}
		contactIds = append(contactIds, contactId)
	} else {
		for _, contactWithLinkedin := range contactsWithLinkedin {
			contactId := utils.GetStringPropOrEmpty(contactWithLinkedin.Props, "id")
			contactIds = append(contactIds, contactId)
		}
	}

	//link contacts to user
	if userId != "" {
		for _, cid := range contactIds {

			isLinkedWith, err := s.commonServices.Neo4jRepositories.CommonReadRepository.IsLinkedWith(ctx, tenant, cid, model.CONTACT, model.CONNECTED_WITH.String(), userId, model.USER)
			if err != nil {
				tracing.TraceErr(span, errors.Wrap(err, "CommonReadRepository.IsLinkedWith"))
				return err
			}

			if !isLinkedWith {
				err = s.commonServices.Neo4jRepositories.CommonWriteRepository.Link(ctx, nil, tenant, neo4jrepository.LinkDetails{
					FromEntityId:   cid,
					FromEntityType: model.CONTACT,
					Relationship:   model.CONNECTED_WITH,
					ToEntityId:     userId,
					ToEntityType:   model.USER,
				})
				if err != nil {
					tracing.TraceErr(span, errors.Wrap(err, "CommonWriteRepository.Link"))
					return err
				}
			}
		}
	}

	pendingLinkedinRequests, err := s.commonServices.Neo4jRepositories.LinkedinConnectionRequestReadRepository.GetPendingRequestByUserForSocialUrl(ctx, nil, tenant, userId, linkedinProfileUrl)
	if err != nil {
		tracing.TraceErr(span, errors.Wrap(err, "LinkedinConnectionRequestReadRepository.GetPendingRequestByUserForSocialUrl"))
		return err
	}

	if pendingLinkedinRequests != nil {
		for _, cid := range contactIds {
			flowActionExecutions, err := s.commonServices.FlowExecutionService.GetFlowActionExecutionsForParticipantWithActionType(ctx, cid, model.CONTACT, neo4jentity.FlowActionTypeLinkedinConnectionRequest)
			if err != nil {
				tracing.TraceErr(span, errors.Wrap(err, "FlowService.FlowGetByParticipant"))
				return err
			}

			if flowActionExecutions != nil && len(flowActionExecutions) > 0 {
				for _, flowActionExecution := range flowActionExecutions {

					_, err := utils.ExecuteWriteInTransaction(ctx, s.commonServices.Neo4jRepositories.Neo4jDriver, s.commonServices.Neo4jRepositories.Database, nil, func(tx neo4j.ManagedTransaction) (any, error) {

						err := s.commonServices.Neo4jRepositories.CommonWriteRepository.UpdateStringProperty(ctx, nil, tenant, model.NodeLabelFlowActionExecution, flowActionExecution.Id, "status", string(neo4jentity.FlowActionExecutionStatusSuccess))
						if err != nil {
							return nil, errors.Wrap(err, "CommonWriteRepository.UpdateStringProperty")
						}

						flowParticipant, err := s.commonServices.FlowService.FlowParticipantByEntity(ctx, flowActionExecution.FlowId, flowActionExecution.EntityId, flowActionExecution.EntityType)
						if err != nil {
							return nil, errors.Wrap(err, "FlowService.FlowParticipantByEntity")
						}

						err = s.commonServices.FlowExecutionService.ScheduleFlow(ctx, nil, flowActionExecution.FlowId, flowParticipant)
						if err != nil {
							return nil, errors.Wrap(err, "FlowService.ScheduleFlow")
						}

						return nil, nil
					})

					if err != nil {
						tracing.TraceErr(span, errors.Wrap(err, "ExecuteWriteInTransaction"))
						return err
					}
				}
			}
		}
	}

	return nil
}

func (s *contactService) linkOrphanContactsToOrganizationBaseOnLinkedinScrapIn(ctx context.Context) {
	span, ctx := tracing.StartTracerSpan(ctx, "ContactService.linkOrphanContactsToOrganizationBaseOnLinkedinScrapIn")
	defer span.Finish()
	tracing.TagComponentCronJob(span)

	orphanContacts, err := s.commonServices.Neo4jRepositories.ContactReadRepository.GetContactsEnrichedNotLinkedToOrganization(ctx)
	if err != nil {
		tracing.TraceErr(span, err)
		return
	}

	span.LogFields(log.Int("orphanContactsCount", len(orphanContacts)))

	for _, orpanContact := range orphanContacts {
		innerCtx := common.WithCustomContext(ctx, &common.CustomContext{
			Tenant:    orpanContact.Tenant,
			AppSource: constants.AppSourceDataUpkeeper,
		})

		tenant := orpanContact.Tenant

		select {
		case <-ctx.Done():
			s.log.Infof("Context cancelled, stopping")
			return
		default:
			// continue as normal
		}

		scrapIn, err := s.commonServices.PostgresRepositories.EnrichDetailsScrapInRepository.GetLatestByParam1AndFlow(ctx, orpanContact.FieldStr1, postgresentity.ScrapInFlowPersonProfile)
		if err != nil {
			tracing.TraceErr(span, errors.Wrap(err, "EnrichDetailsScrapInRepository.GetLatestByParam1AndFlow"))
			return
		}

		if scrapIn != nil && scrapIn.Success && scrapIn.CompanyFound {

			var scrapinContactResponse postgresentity.ScrapInResponseBody
			err := json.Unmarshal([]byte(scrapIn.Data), &scrapinContactResponse)
			if err != nil {
				tracing.TraceErr(span, errors.Wrap(err, "json.Unmarshal"))
				return
			}

			domain, _ := s.commonServices.DomainService.GetPrimaryDomainForOrganizationWebsite(ctx, scrapinContactResponse.Company.WebsiteUrl)
			if domain == "" {
				continue
			}

			organizationByDomainNode, err := s.commonServices.Neo4jRepositories.OrganizationReadRepository.GetOrganizationByDomain(ctx, tenant, domain)
			if err != nil {
				//TODO uncomment when data is fixed in DB
				//tracing.TraceErr(span, errors.Wrap(err, "OrganizationReadRepository.GetOrganizationByDomain"))
				//return
				continue
			}

			if organizationByDomainNode != nil {
				organizationId := utils.GetStringPropOrEmpty(organizationByDomainNode.Props, "id")

				positionName := ""
				if len(scrapinContactResponse.Person.Positions.PositionHistory) > 0 {
					for _, position := range scrapinContactResponse.Person.Positions.PositionHistory {
						if position.Title != "" && position.CompanyName != "" && position.CompanyName == scrapinContactResponse.Company.Name {
							positionName = position.Title
							break
						}
					}
				}

				err = s.commonServices.ContactService.LinkContactWithOrganization(innerCtx, orpanContact.ContactId, organizationId, positionName, "",
					neo4jentity.DataSourceOpenline.String(), false, nil, nil)
				if err != nil {
					tracing.TraceErr(span, errors.Wrap(err, "ContactService.LinkContactWithOrganization"))
				}
			}
		}
	}
}

func (s *contactService) findEmailsWithBetterContact(ctx context.Context) {
	span, ctx := tracing.StartTracerSpan(ctx, "ContactService.findEmailsWithBetterContact")
	defer span.Finish()
	tracing.TagComponentCronJob(span)

	// TODO re-enable job based on additional settings if needed.
	// add billable event for better contact response
	return

	// Better contact is limited to 60 requests per minute
	// https://bettercontact.notion.site/Documentation-API-e8e1b352a0d647ee9ff898609bf1a168
	limit := 50

	for {
		select {
		case <-ctx.Done():
			s.log.Infof("Context cancelled, stopping")
			return
		default:
			// continue as normal
		}

		minutesFromLastContactUpdate := 2
		records, err := s.commonServices.Neo4jRepositories.ContactReadRepository.GetContactsToFindWorkEmailWithBetterContact(ctx, minutesFromLastContactUpdate, limit)
		if err != nil {
			tracing.TraceErr(span, err)
			return
		}

		// no record
		if len(records) == 0 {
			return
		}

		for _, record := range records {
			enrichmentResponse, err := s.callEnrichmentApiFindWorkEmail(ctx, record)
			if err != nil {
				tracing.TraceErr(span, err)
				span.LogFields(log.Object("record", record))
			} else {
				// mark contact with enrich requested
				err = s.commonServices.Neo4jRepositories.ContactWriteRepository.UpdateAnyProperty(ctx, record.Tenant, record.ContactId, neo4jentity.ContactPropertyFindWorkEmailWithBetterContactRequestedId, enrichmentResponse.BetterContactRequestId)
				if err != nil {
					tracing.TraceErr(span, err)
				}
				err = s.commonServices.Neo4jRepositories.CommonWriteRepository.UpdateTimeProperty(ctx, record.Tenant, model.NodeLabelContact, record.ContactId, string(neo4jentity.ContactPropertyFindWorkEmailWithBetterContactRequestedAt), utils.NowPtr())
				if err != nil {
					tracing.TraceErr(span, err)
				}
			}
		}

		// if less than limit records are returned, we are done
		if len(records) < limit {
			return
		}

		// force exit after single iteration
		return
	}
}

func (s *contactService) callEnrichmentApiFindWorkEmail(ctx context.Context, details neo4jrepository.ContactsEnrichWorkEmail) (*enrichmentmodel.FindWorkEmailResponse, error) {
	span, ctx := tracing.StartTracerSpan(ctx, "ContactService.callEnrichmentApiFindWorkEmail")
	defer span.Finish()

	requestJSON, err := json.Marshal(enrichmentmodel.FindWorkEmailRequest{
		LinkedinUrl:   details.LinkedInUrl,
		FirstName:     details.ContactFirstName,
		LastName:      details.ContactLastName,
		CompanyName:   details.OrganizationName,
		CompanyDomain: details.OrganizationDomain,
	})
	if err != nil {
		tracing.TraceErr(span, errors.Wrap(err, "failed to marshal request"))
		return nil, err
	}
	requestBody := []byte(string(requestJSON))
	req, err := http.NewRequest("GET", s.cfg.EnrichmentApi.Url+"/findWorkEmail", bytes.NewBuffer(requestBody))
	if err != nil {
		tracing.TraceErr(span, errors.Wrap(err, "failed to create request"))
		return nil, err
	}
	// Inject span context into the HTTP request
	req = tracing.InjectSpanContextIntoHTTPRequest(req, span)

	// Set the request headers
	req.Header.Set(security.ApiKeyHeader, s.cfg.EnrichmentApi.ApiKey)
	req.Header.Set(security.TenantHeader, details.Tenant)

	// Make the HTTP request
	client := &http.Client{}
	response, err := client.Do(req)
	if err != nil {
		tracing.TraceErr(span, errors.Wrap(err, "failed to perform request"))
		return nil, err
	}
	defer response.Body.Close()
	span.LogFields(log.Int("response.status.findWorkEmail", response.StatusCode))

	var findWorkEmailApiResponse enrichmentmodel.FindWorkEmailResponse
	err = json.NewDecoder(response.Body).Decode(&findWorkEmailApiResponse)
	if err != nil {
		tracing.TraceErr(span, errors.Wrap(err, "failed to decode find work email response"))
		return nil, err
	}
	return &findWorkEmailApiResponse, nil
}

func (s *contactService) EnrichWithWorkEmailFromBetterContact() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Cancel context on exit

	span, ctx := tracing.StartTracerSpan(ctx, "ContactService.EnrichWithWorkEmailFromBetterContact")
	defer span.Finish()
	tracing.TagComponentCronJob(span)

	limit := 250

	records, err := s.commonServices.Neo4jRepositories.ContactReadRepository.GetContactsToEnrichWithEmailFromBetterContact(ctx, limit)
	if err != nil {
		tracing.TraceErr(span, err)
		return
	}

	for _, record := range records {
		// create new context from main one with custom context
		innerCtx := common.WithCustomContext(ctx, &common.CustomContext{
			Tenant:    record.Tenant,
			AppSource: constants.AppSourceDataUpkeeper,
		})

		// mark contact with update requested
		err = s.commonServices.Neo4jRepositories.CommonWriteRepository.UpdateTimeProperty(innerCtx, record.Tenant, model.NodeLabelContact, record.ContactId, string(neo4jentity.ContactPropertyUpdateWithWorkEmailRequestedAt), utils.NowPtr())
		if err != nil {
			tracing.TraceErr(span, err)
		}

		detailsBetterContact, err := s.commonServices.PostgresRepositories.EnrichDetailsBetterContactRepository.GetByRequestId(innerCtx, record.FieldStr1)
		if err != nil {
			tracing.TraceErr(span, err)
			return
		}

		if detailsBetterContact == nil {
			tracing.TraceErr(span, errors.New("better contact details by request id not found"))

			detailsBetterContact, err = s.commonServices.PostgresRepositories.EnrichDetailsBetterContactRepository.GetById(innerCtx, record.FieldStr1)
			if err != nil {
				tracing.TraceErr(span, err)
				return
			}

			if detailsBetterContact == nil {
				tracing.TraceErr(span, errors.New("better contact details by id not found"))
				continue
			}
		}

		if detailsBetterContact.Response == "" {
			continue
		}

		var betterContactResponse postgresentity.BetterContactResponseBody
		if err = json.Unmarshal([]byte(detailsBetterContact.Response), &betterContactResponse); err != nil {
			tracing.TraceErr(span, err)
			return
		}

		// current emails linked with contacts
		var currentEmails []string
		emailDbNodes, err := s.commonServices.Neo4jRepositories.EmailReadRepository.GetAllEmailNodesForLinkedEntityIds(innerCtx, record.Tenant, model.CONTACT, []string{record.ContactId})
		if err != nil {
			tracing.TraceErr(span, err)
		}
		for _, emailDbNode := range emailDbNodes {
			emailEntity := neo4jmapper.MapDbNodeToEmailEntity(emailDbNode.Node)
			if emailEntity.RawEmail != "" {
				currentEmails = append(currentEmails, emailEntity.RawEmail)
			}
		}

		// current phone numbers linked with contacts
		var currentPhones []string
		phoneDbNodes, err := s.commonServices.Neo4jRepositories.PhoneNumberReadRepository.GetAllForLinkedEntityIds(innerCtx, record.Tenant, model.CONTACT, []string{record.ContactId})
		if err != nil {
			tracing.TraceErr(span, err)
		}
		for _, phoneDbNode := range phoneDbNodes {
			phoneEntity := neo4jmapper.MapDbNodeToPhoneNumberEntity(phoneDbNode.Node)
			if phoneEntity.RawPhoneNumber != "" {
				currentPhones = append(currentPhones, phoneEntity.RawPhoneNumber)
			}
			if phoneEntity.E164 != "" {
				currentPhones = append(currentPhones, phoneEntity.E164)
			}
		}

		emailLinked := false
		phoneLinked := false
		emailForBillableEvent, phoneNumberForBillableEvent := "", ""
		if len(betterContactResponse.Data) > 0 {
			for _, item := range betterContactResponse.Data {
				if item.ContactEmailAddress != "" && !utils.Contains(currentEmails, item.ContactEmailAddress) {
					_, err = s.commonServices.EmailService.Merge(innerCtx, record.Tenant,
						commonService.EmailFields{
							Email:     item.ContactEmailAddress,
							AppSource: constants.AppSourceDataUpkeeper,
						},
						&commonService.LinkWith{
							Type: model.CONTACT,
							Id:   record.ContactId,
						})
					if err != nil {
						tracing.TraceErr(span, err)
						continue
					}
					emailLinked = true
					if emailForBillableEvent == "" {
						emailForBillableEvent = item.ContactEmailAddress
					}
				}
				if item.ContactPhoneNumber != nil {
					phoneNumber := fmt.Sprintf("%v", item.ContactPhoneNumber)
					if phoneNumber != "" && !utils.Contains(currentPhones, phoneNumber) {
						phoneLinked = true
						// create phone number
						eventCtx := tracing.InjectSpanContextIntoGrpcMetadata(innerCtx, span)
						response, err := utils.CallEventsPlatformGRPCWithRetry[*phonenumberpb.PhoneNumberIdGrpcResponse](func() (*phonenumberpb.PhoneNumberIdGrpcResponse, error) {
							return s.commonServices.GrpcClients.PhoneNumberClient.UpsertPhoneNumber(eventCtx, &phonenumberpb.UpsertPhoneNumberGrpcRequest{
								Tenant:      record.Tenant,
								PhoneNumber: phoneNumber,
								SourceFields: &commonpb.SourceFields{
									Source:    string(neo4jentity.DataSourceOpenline),
									AppSource: constants.AppSourceDataUpkeeper,
								},
							})
						})
						if err != nil {
							tracing.TraceErr(span, err)
							s.log.Errorf("Error from events processing %s", err.Error())
							continue
						}

						neo4jrepository.WaitForNodeCreatedInNeo4j(innerCtx, s.commonServices.Neo4jRepositories, response.Id, model.NodeLabelPhoneNumber, span)

						// link with contact
						if response.Id != "" {
							_, err = utils.CallEventsPlatformGRPCWithRetry[*contactpb.ContactIdGrpcResponse](func() (*contactpb.ContactIdGrpcResponse, error) {
								return s.commonServices.GrpcClients.ContactClient.LinkPhoneNumberToContact(eventCtx, &contactpb.LinkPhoneNumberToContactGrpcRequest{
									Tenant:        record.Tenant,
									ContactId:     record.ContactId,
									PhoneNumberId: response.Id,
									Primary:       false,
									AppSource:     constants.AppSourceDataUpkeeper,
								})
							})
							if err != nil {
								tracing.TraceErr(span, err)
								s.log.Errorf("Error from events processing %s", err.Error())
								continue
							}
						}
						if phoneNumberForBillableEvent == "" {
							phoneNumberForBillableEvent = phoneNumber
						}
					}
				}
			}
		}

		if emailLinked {
			_, err = s.commonServices.PostgresRepositories.ApiBillableEventRepository.RegisterEvent(innerCtx, record.Tenant, postgresentity.BillableEventEnrichPersonEmailFound,
				postgresrepository.BillableEventDetails{
					ExternalID:    betterContactResponse.Id,
					ReferenceData: fmt.Sprintf("Email: %s, LinkedIn: %s, FirstName: %s, LastName: %s", emailForBillableEvent, detailsBetterContact.ContactLinkedInUrl, detailsBetterContact.ContactFirstName, detailsBetterContact.ContactLastName),
				},
			)
			if err != nil {
				tracing.TraceErr(span, errors.Wrap(err, "failed to store billable event"))
			}
		}
		if phoneLinked {
			_, err = s.commonServices.PostgresRepositories.ApiBillableEventRepository.RegisterEvent(innerCtx, record.Tenant, postgresentity.BillableEventEnrichPersonPhoneFound,
				postgresrepository.BillableEventDetails{
					ExternalID:    betterContactResponse.Id,
					ReferenceData: fmt.Sprintf("Phone: %s, LinkedIn: %s, FirstName: %s, LastName: %s", phoneNumberForBillableEvent, detailsBetterContact.ContactLinkedInUrl, detailsBetterContact.ContactFirstName, detailsBetterContact.ContactLastName),
				},
			)
			if err != nil {
				tracing.TraceErr(span, errors.Wrap(err, "failed to store billable event"))
			}
		}

		// mark contact enrich fields for email
		err = s.commonServices.Neo4jRepositories.CommonWriteRepository.UpdateTimeProperty(innerCtx, record.Tenant, model.NodeLabelContact, record.ContactId, string(neo4jentity.ContactPropertyFindWorkEmailWithBetterContactCompletedAt), utils.NowPtr())
		if err != nil {
			tracing.TraceErr(span, err)
		}
		if emailLinked {
			err = s.commonServices.Neo4jRepositories.CommonWriteRepository.UpdateBoolProperty(innerCtx, record.Tenant, model.NodeLabelContact, record.ContactId, string(neo4jentity.ContactPropertyFindWorkEmailWithBetterContactFound), true)
			if err != nil {
				tracing.TraceErr(span, err)
			}
		} else {
			err = s.commonServices.Neo4jRepositories.CommonWriteRepository.UpdateBoolProperty(innerCtx, record.Tenant, model.NodeLabelContact, record.ContactId, string(neo4jentity.ContactPropertyFindWorkEmailWithBetterContactFound), false)
			if err != nil {
				tracing.TraceErr(span, err)
			}
		}

		// mark contact enrich fields for phone number
		if detailsBetterContact.EnrichPhoneNumber {
			err = s.commonServices.Neo4jRepositories.CommonWriteRepository.UpdateTimeProperty(innerCtx, record.Tenant, model.NodeLabelContact, record.ContactId, string(neo4jentity.ContactPropertyFindMobilePhoneWithBetterContactCompletedAt), utils.NowPtr())
			if err != nil {
				tracing.TraceErr(span, err)
			}
			if phoneLinked {
				err = s.commonServices.Neo4jRepositories.CommonWriteRepository.UpdateBoolProperty(innerCtx, record.Tenant, model.NodeLabelContact, record.ContactId, string(neo4jentity.ContactPropertyFindMobilePhoneWithBetterContactFound), true)
				if err != nil {
					tracing.TraceErr(span, err)
				}
			} else {
				err = s.commonServices.Neo4jRepositories.CommonWriteRepository.UpdateBoolProperty(innerCtx, record.Tenant, model.NodeLabelContact, record.ContactId, string(neo4jentity.ContactPropertyFindMobilePhoneWithBetterContactFound), false)
				if err != nil {
					tracing.TraceErr(span, err)
				}
			}
		}
		utils.EventCompleted(ctx, record.Tenant, model.CONTACT.String(), record.ContactId, s.commonServices.GrpcClients, utils.NewEventCompletedDetails().WithUpdate())
	}
}

func (s *contactService) checkBetterContactRequestsWithoutResponse(ctx context.Context) {
	span, ctx := tracing.StartTracerSpan(ctx, "ContactService.checkBetterContactRequestsWithoutResponse")
	defer span.Finish()
	tracing.TagComponentCronJob(span)

	betterContactRequestsWithoutResponse, err := s.commonServices.PostgresRepositories.EnrichDetailsBetterContactRepository.GetWithoutResponses(ctx)
	if err != nil {
		tracing.TraceErr(span, err)
		return
	}

	for _, record := range betterContactRequestsWithoutResponse {

		// Create HTTP client
		client := &http.Client{}

		// Create POST request
		req, err := http.NewRequest("GET", fmt.Sprintf("%s?api_key=%s", s.cfg.BetterContactApi.Url+"/"+record.RequestID, s.cfg.BetterContactApi.ApiKey), nil)
		if err != nil {
			tracing.TraceErr(span, err)
			return
		}

		// Set headers
		req.Header.Set("Content-Type", "application/json")

		//Perform the request
		resp, err := client.Do(req)
		if err != nil {
			tracing.TraceErr(span, err)
			return
		}
		defer resp.Body.Close()

		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			tracing.TraceErr(span, err)
			return
		}

		if responseBody == nil || string(responseBody) == "Retry later" {
			return
		}

		// Parse the JSON request body
		var betterContactResponse postgresentity.BetterContactResponseBody
		if err = json.Unmarshal(responseBody, &betterContactResponse); err != nil {
			tracing.TraceErr(span, err)
			return
		}

		if betterContactResponse.Status == "terminated" {
			err = s.commonServices.PostgresRepositories.EnrichDetailsBetterContactRepository.AddResponse(ctx, record.RequestID, string(responseBody))
			if err != nil {
				tracing.TraceErr(span, err)
				return
			}
			// store billable events
			// first check if it was requested externally
			personEnrichmentRequest, err := s.commonServices.PostgresRepositories.CosApiEnrichPersonTempResultRepository.GetByBettercontactRecordId(ctx, betterContactResponse.Id)
			if err != nil {
				tracing.TraceErr(span, errors.Wrap(err, "failed to check if bettercontact record was requested from person enrichment"))
			} else if personEnrichmentRequest != nil {
				emailFound, phoneFound := false, false
				for _, item := range betterContactResponse.Data {
					if item.ContactEmailAddress != "" {
						emailFound = true
					}
					if item.ContactPhoneNumber != nil && fmt.Sprintf("%v", item.ContactPhoneNumber) != "" {
						phoneFound = true
					}
				}
				if emailFound {
					_, err = s.commonServices.PostgresRepositories.ApiBillableEventRepository.RegisterEvent(ctx, personEnrichmentRequest.Tenant, postgresentity.BillableEventEnrichPersonEmailFound,
						postgresrepository.BillableEventDetails{
							ExternalID:    personEnrichmentRequest.BettercontactRecordId,
							ReferenceData: "generated in upkeeper",
						})
					if err != nil {
						tracing.TraceErr(span, errors.Wrap(err, "failed to store billable event"))
					}
				}
				if phoneFound {
					_, err = s.commonServices.PostgresRepositories.ApiBillableEventRepository.RegisterEvent(ctx, personEnrichmentRequest.Tenant, postgresentity.BillableEventEnrichPersonPhoneFound,
						postgresrepository.BillableEventDetails{
							ExternalID:    personEnrichmentRequest.BettercontactRecordId,
							ReferenceData: "generated in upkeeper",
						})
					if err != nil {
						tracing.TraceErr(span, errors.Wrap(err, "failed to store billable event"))
					}
				}
			}
		}
	}
}

func (s *contactService) EnrichContacts() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Cancel context on exit

	s.enrichContacts(ctx)
}

func (s *contactService) enrichContacts(ctx context.Context) {
	span, ctx := tracing.StartTracerSpan(ctx, "ContactService.enrichContacts")
	defer span.Finish()
	tracing.TagComponentCronJob(span)

	limit := 20

	for {
		select {
		case <-ctx.Done():
			s.log.Infof("Context cancelled, stopping")
			return
		default:
			// continue as normal
		}

		minutesFromLastContactUpdate := 2
		minutesFromLastContactEnrichAttempt := 1 * 24 * 60 // 1 day
		minutesFromLastFailure := 10 * 24 * 60             // 10 days
		records, err := s.commonServices.Neo4jRepositories.ContactReadRepository.GetContactsToEnrich(ctx, minutesFromLastContactUpdate, minutesFromLastContactEnrichAttempt, minutesFromLastFailure, limit)
		if err != nil {
			tracing.TraceErr(span, err)
			s.log.Errorf("Error getting socials: %v", err)
			return
		}

		// no record
		if len(records) == 0 {
			return
		}

		for _, record := range records {
			// create new context from main one with custom context
			innerCtx := common.WithCustomContext(ctx, &common.CustomContext{
				Tenant:    record.Tenant,
				AppSource: constants.AppSourceDataUpkeeper,
			})

			err = s.commonServices.RabbitMQService.PublishEvent(innerCtx, record.ContactId, model.CONTACT, dto.RequestEnrichContact{})
			if err != nil {
				tracing.TraceErr(span, errors.Wrap(err, "unable to publish message RequestEnrichContact"))
				s.log.Errorf("Error requesting enrich contact {%s}: %s", record.ContactId, err.Error())
			}

			// mark contact with enrich requested
			err = s.commonServices.Neo4jRepositories.CommonWriteRepository.UpdateTimeProperty(innerCtx, record.Tenant, model.NodeLabelContact, record.ContactId, string(neo4jentity.ContactPropertyEnrichRequestedAt), utils.NowPtr())
			if err != nil {
				tracing.TraceErr(span, err)
				s.log.Errorf("Error updating contact' enrich requested: %s", err.Error())
			}

			// increment enrich attempts
			err = s.commonServices.Neo4jRepositories.CommonWriteRepository.IncrementProperty(innerCtx, record.Tenant, model.NodeLabelContact, record.ContactId, string(neo4jentity.ContactPropertyEnrichAttempts))
			if err != nil {
				tracing.TraceErr(span, err)
				s.log.Errorf("Error incrementing contact' enrich attempts: %s", err.Error())
			}
		}

		// if less than limit records are returned, we are done
		if len(records) < limit {
			return
		}

		// force exit after single iteration
		return
	}
}
