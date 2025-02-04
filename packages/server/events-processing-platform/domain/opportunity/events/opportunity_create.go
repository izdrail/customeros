package events

import (
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/validator"
	"github.com/openline-ai/openline-customer-os/packages/server/events-processing-platform/domain/opportunity/model"
	commonmodel "github.com/openline-ai/openline-customer-os/packages/server/events/event/common"
	opportunityevent "github.com/openline-ai/openline-customer-os/packages/server/events/event/opportunity"
	"github.com/openline-ai/openline-customer-os/packages/server/events/eventstore"
	"github.com/pkg/errors"
	"time"
)

type OpportunityCreateEvent struct {
	Tenant            string                     `json:"tenant" validate:"required"`
	Name              string                     `json:"name"`
	MaxAmount         float64                    `json:"maxAmount"`
	InternalType      string                     `json:"internalType"`
	ExternalType      string                     `json:"externalType"`
	InternalStage     string                     `json:"internalStage"`
	ExternalStage     string                     `json:"externalStage"`
	EstimatedClosedAt *time.Time                 `json:"estimatedClosedAt,omitempty"`
	OwnerUserId       string                     `json:"ownerUserId"`
	CreatedByUserId   string                     `json:"createdByUserId"`
	CreatedAt         time.Time                  `json:"createdAt"`
	UpdatedAt         time.Time                  `json:"updatedAt"`
	Source            commonmodel.Source         `json:"source"`
	ExternalSystem    commonmodel.ExternalSystem `json:"externalSystem,omitempty"`
	OrganizationId    string                     `json:"organizationId" validate:"required"`
	GeneralNotes      string                     `json:"generalNotes"`
	NextSteps         string                     `json:"nextSteps"`
	Currency          string                     `json:"currency"`
	LikelihoodRate    int64                      `json:"likelihoodRate"`
}

func NewOpportunityCreateEvent(aggregate eventstore.Aggregate, dataFields model.OpportunityDataFields, source commonmodel.Source, externalSystem commonmodel.ExternalSystem, createdAt, updatedAt time.Time) (eventstore.Event, error) {
	eventData := OpportunityCreateEvent{
		Tenant:            aggregate.GetTenant(),
		Name:              dataFields.Name,
		MaxAmount:         dataFields.MaxAmount,
		InternalType:      string(dataFields.InternalType.StringEnumValue()),
		ExternalType:      dataFields.ExternalType,
		InternalStage:     string(dataFields.InternalStage.StringEnumValue()),
		ExternalStage:     dataFields.ExternalStage,
		EstimatedClosedAt: dataFields.EstimatedClosedAt,
		OwnerUserId:       dataFields.OwnerUserId,
		CreatedByUserId:   dataFields.CreatedByUserId,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
		Source:            source,
		OrganizationId:    dataFields.OrganizationId,
		GeneralNotes:      dataFields.GeneralNotes,
		NextSteps:         dataFields.NextSteps,
		Currency:          dataFields.Currency,
		LikelihoodRate:    dataFields.LikelihoodRate,
	}
	if externalSystem.Available() {
		eventData.ExternalSystem = externalSystem
	}

	if err := validator.GetValidator().Struct(eventData); err != nil {
		return eventstore.Event{}, errors.Wrap(err, "failed to validate OpportunityCreateEvent")
	}

	event := eventstore.NewBaseEvent(aggregate, opportunityevent.OpportunityCreateV1)
	if err := event.SetJsonData(&eventData); err != nil {
		return eventstore.Event{}, errors.Wrap(err, "error setting json data for OpportunityCreateEvent")
	}
	return event, nil
}
