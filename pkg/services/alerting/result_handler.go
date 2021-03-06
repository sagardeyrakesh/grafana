package alerting

import (
	"time"

	"github.com/grafana/grafana/pkg/bus"
	"github.com/grafana/grafana/pkg/components/simplejson"
	"github.com/grafana/grafana/pkg/log"
	"github.com/grafana/grafana/pkg/metrics"
	m "github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/annotations"
)

type ResultHandler interface {
	Handle(evalContext *EvalContext) error
}

type DefaultResultHandler struct {
	notifier Notifier
	log      log.Logger
}

func NewResultHandler() *DefaultResultHandler {
	return &DefaultResultHandler{
		log:      log.New("alerting.resultHandler"),
		notifier: NewRootNotifier(),
	}
}

func (handler *DefaultResultHandler) Handle(evalContext *EvalContext) error {
	oldState := evalContext.Rule.State

	executionError := ""
	annotationData := simplejson.New()
	if evalContext.Error != nil {
		handler.log.Error("Alert Rule Result Error", "ruleId", evalContext.Rule.Id, "error", evalContext.Error)
		evalContext.Rule.State = m.AlertStateExecError
		executionError = evalContext.Error.Error()
		annotationData.Set("errorMessage", executionError)
	} else if evalContext.Firing {
		evalContext.Rule.State = m.AlertStateAlerting
		annotationData = simplejson.NewFromAny(evalContext.EvalMatches)
	} else {
		if evalContext.NoDataFound {
			if evalContext.Rule.NoDataState != m.NoDataKeepState {
				evalContext.Rule.State = evalContext.Rule.NoDataState.ToAlertState()
			}
		} else {
			evalContext.Rule.State = m.AlertStateOK
		}
	}

	countStateResult(evalContext.Rule.State)
	if handler.shouldUpdateAlertState(evalContext, oldState) {
		handler.log.Info("New state change", "alertId", evalContext.Rule.Id, "newState", evalContext.Rule.State, "oldState", oldState)

		cmd := &m.SetAlertStateCommand{
			AlertId:  evalContext.Rule.Id,
			OrgId:    evalContext.Rule.OrgId,
			State:    evalContext.Rule.State,
			Error:    executionError,
			EvalData: annotationData,
		}

		if err := bus.Dispatch(cmd); err != nil {
			handler.log.Error("Failed to save state", "error", err)
		}

		// save annotation
		item := annotations.Item{
			OrgId:       evalContext.Rule.OrgId,
			DashboardId: evalContext.Rule.DashboardId,
			PanelId:     evalContext.Rule.PanelId,
			Type:        annotations.AlertType,
			AlertId:     evalContext.Rule.Id,
			Title:       evalContext.Rule.Name,
			Text:        evalContext.GetStateModel().Text,
			NewState:    string(evalContext.Rule.State),
			PrevState:   string(oldState),
			Epoch:       time.Now().Unix(),
			Data:        annotationData,
		}

		annotationRepo := annotations.GetRepository()
		if err := annotationRepo.Save(&item); err != nil {
			handler.log.Error("Failed to save annotation for new alert state", "error", err)
		}

		if (oldState == m.AlertStatePending) && (evalContext.Rule.State == m.AlertStateOK) {
			handler.log.Info("Notfication not sent", "oldState", oldState, "newState", evalContext.Rule.State)
		} else {
			handler.notifier.Notify(evalContext)
		}

	}

	return nil
}

func (handler *DefaultResultHandler) shouldUpdateAlertState(evalContext *EvalContext, oldState m.AlertStateType) bool {
	return evalContext.Rule.State != oldState
}

func countStateResult(state m.AlertStateType) {
	switch state {
	case m.AlertStatePending:
		metrics.M_Alerting_Result_State_Pending.Inc(1)
	case m.AlertStateAlerting:
		metrics.M_Alerting_Result_State_Alerting.Inc(1)
	case m.AlertStateOK:
		metrics.M_Alerting_Result_State_Ok.Inc(1)
	case m.AlertStatePaused:
		metrics.M_Alerting_Result_State_Paused.Inc(1)
	case m.AlertStateNoData:
		metrics.M_Alerting_Result_State_NoData.Inc(1)
	case m.AlertStateExecError:
		metrics.M_Alerting_Result_State_ExecError.Inc(1)
	}
}
