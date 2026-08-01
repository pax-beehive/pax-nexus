package handler

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/cohorttask"
	api "github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/transport/httpapi/model/knowledgeeval/api"
)

func (h *Handler) PreviewCohortCampaign(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalCohortCampaignRequest,
) {
	cohorts, ok := h.cohortService(c)
	if !ok {
		return
	}
	preview, err := cohorts.Preview(ctx, fromAPICohortRequest(request))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, &api.KnowledgeEvalCohortCampaignPreviewResponse{
		Preview: toAPICohortPreview(preview),
	})
}

func (h *Handler) CreateCohortCampaign(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalCohortCampaignRequest,
) {
	cohorts, ok := h.cohortService(c)
	if !ok {
		return
	}
	campaign, err := cohorts.Create(
		ctx,
		fromAPICohortRequest(request),
		strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, &api.KnowledgeEvalCohortCampaignResponse{
		Campaign: toAPICohortCampaign(campaign),
	})
}

func (h *Handler) ListCohortCampaigns(
	_ context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalListRequest,
) {
	cohorts, ok := h.cohortService(c)
	if !ok {
		return
	}
	items := cohorts.List()
	if status := request.GetStatus(); status != "" {
		filtered := make([]cohorttask.Campaign, 0, len(items))
		for _, campaign := range items {
			if campaign.Status == status {
				filtered = append(filtered, campaign)
			}
		}
		items = filtered
	}
	pageItems, page, err := paginate(items, request.GetLimit(), request.GetCursor())
	if err != nil {
		writeError(c, err)
		return
	}
	response := &api.KnowledgeEvalCohortCampaignsResponse{
		Items: make([]*api.KnowledgeEvalCohortCampaign, 0, len(pageItems)),
		Page:  page,
	}
	for _, campaign := range pageItems {
		response.Items = append(response.Items, toAPICohortCampaignSummary(campaign))
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) GetCohortCampaign(
	_ context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalCohortCampaignByIDRequest,
) {
	cohorts, ok := h.cohortService(c)
	if !ok {
		return
	}
	campaign, err := cohorts.Get(request.CampaignID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, &api.KnowledgeEvalCohortCampaignResponse{
		Campaign: toAPICohortCampaign(campaign),
	})
}

func (h *Handler) CancelCohortCampaign(
	_ context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalCohortCampaignByIDRequest,
) {
	cohorts, ok := h.cohortService(c)
	if !ok {
		return
	}
	campaign, err := cohorts.Cancel(request.CampaignID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, &api.KnowledgeEvalCohortCampaignResponse{
		Campaign: toAPICohortCampaign(campaign),
	})
}

func (h *Handler) cohortService(c *app.RequestContext) (CohortService, bool) {
	if h.cohorts == nil {
		c.JSON(consts.StatusServiceUnavailable, map[string]string{
			"error": "knowledge eval cohort campaigns are not configured",
		})
		return nil, false
	}
	return h.cohorts, true
}

func fromAPICohortRequest(request *api.KnowledgeEvalCohortCampaignRequest) cohorttask.Request {
	selections := make([]cohorttask.DatasetSelection, 0, len(request.GetSelections()))
	for _, selection := range request.GetSelections() {
		if selection == nil {
			continue
		}
		selections = append(selections, cohorttask.DatasetSelection{
			Dataset: selection.Dataset, Partition: selection.Partition,
		})
	}
	recipe := request.GetRecipe()
	result := cohorttask.Request{
		Name: request.Name, Selections: selections,
		ConfirmPaid: request.GetConfirmPaid(), LLMCallLimit: int(request.GetLlmCallLimit()),
	}
	if recipe != nil {
		result.Recipe = cohorttask.Recipe{
			Mode: recipe.Mode, Model: recipe.GetModel(),
			ReaderModel: recipe.GetReaderModel(), MaxRounds: int(recipe.GetMaxRounds()),
		}
	}
	return result
}

func toAPICohortCampaign(campaign cohorttask.Campaign) *api.KnowledgeEvalCohortCampaign {
	result := &api.KnowledgeEvalCohortCampaign{
		ID: campaign.ID, Request: toAPICohortRequest(campaign.Request),
		Preview: toAPICohortPreview(campaign.Preview), Status: campaign.Status,
		Summary:               toAPICohortSummary(campaign.Summary),
		Executions:            make([]*api.KnowledgeEvalCohortExecution, 0, len(campaign.Executions)),
		CreatedAt:             campaign.CreatedAt.Format(timeLayout),
		UpdatedAt:             campaign.UpdatedAt.Format(timeLayout),
		CancellationRequested: campaign.CancellationRequested,
	}
	if campaign.CompletedAt != nil {
		result.CompletedAt = optionalString(campaign.CompletedAt.Format(timeLayout))
	}
	for _, execution := range campaign.Executions {
		result.Executions = append(result.Executions, toAPICohortExecution(execution))
	}
	return result
}

func toAPICohortCampaignSummary(
	campaign cohorttask.Campaign,
) *api.KnowledgeEvalCohortCampaign {
	result := toAPICohortCampaign(campaign)
	result.Executions = []*api.KnowledgeEvalCohortExecution{}
	return result
}

func toAPICohortRequest(request cohorttask.Request) *api.KnowledgeEvalCohortCampaignRequest {
	result := &api.KnowledgeEvalCohortCampaignRequest{
		Name:       request.Name,
		Selections: make([]*api.KnowledgeEvalCohortDatasetSelection, 0, len(request.Selections)),
		Recipe:     &api.KnowledgeEvalCohortRecipe{Mode: request.Recipe.Mode},
	}
	for _, selection := range request.Selections {
		result.Selections = append(result.Selections, &api.KnowledgeEvalCohortDatasetSelection{
			Dataset: selection.Dataset, Partition: selection.Partition,
		})
	}
	result.Recipe.Model = optionalString(request.Recipe.Model)
	result.Recipe.ReaderModel = optionalString(request.Recipe.ReaderModel)
	maxRounds := int32(request.Recipe.MaxRounds)
	result.Recipe.MaxRounds = &maxRounds
	result.ConfirmPaid = &request.ConfirmPaid
	callLimit := int64(request.LLMCallLimit)
	result.LlmCallLimit = &callLimit
	return result
}

func toAPICohortPreview(preview cohorttask.Preview) *api.KnowledgeEvalCohortCampaignPreview {
	result := &api.KnowledgeEvalCohortCampaignPreview{
		Eligible: preview.Eligible, Paid: preview.Paid,
		TotalGroups: int32(preview.TotalGroups), EligibleGroups: int32(preview.EligibleGroups),
		IneligibleGroups: int32(preview.IneligibleGroups),
		TotalQuestions:   int32(preview.TotalQuestions),
		PlannedQuestions: int32(preview.PlannedQuestions), PlannedTasks: int32(preview.PlannedTasks),
		MaxLlmCalls: int64(preview.MaxLLMCalls),
		Datasets:    make([]*api.KnowledgeEvalCohortDatasetPreview, 0, len(preview.Datasets)),
		Issues:      make([]*api.KnowledgeEvalCohortIssue, 0, len(preview.Issues)),
	}
	for _, dataset := range preview.Datasets {
		result.Datasets = append(result.Datasets, &api.KnowledgeEvalCohortDatasetPreview{
			Dataset: dataset.Dataset, Partition: dataset.Partition,
			TotalGroups: int32(dataset.TotalGroups), EligibleGroups: int32(dataset.EligibleGroups),
			IneligibleGroups: int32(dataset.IneligibleGroups),
			TotalQuestions:   int32(dataset.TotalQuestions),
			PlannedQuestions: int32(dataset.PlannedQuestions),
			MaxLlmCalls:      int64(dataset.MaxLLMCalls),
		})
	}
	for _, issue := range preview.Issues {
		result.Issues = append(result.Issues, &api.KnowledgeEvalCohortIssue{
			Dataset: issue.Dataset, Partition: issue.Partition,
			GroupID: optionalString(issue.GroupID), Reason: issue.Reason,
		})
	}
	return result
}

func toAPICohortSummary(summary cohorttask.Summary) *api.KnowledgeEvalCohortSummary {
	return &api.KnowledgeEvalCohortSummary{
		TotalGroups: int32(summary.TotalGroups), EligibleGroups: int32(summary.EligibleGroups),
		EvaluatedGroups: int32(summary.EvaluatedGroups), FailedGroups: int32(summary.FailedGroups),
		TotalQuestions:     int32(summary.TotalQuestions),
		EvaluatedQuestions: int32(summary.EvaluatedQuestions),
		CorrectQuestions:   int32(summary.CorrectQuestions),
		MicroAccuracy:      summary.MicroAccuracy, MacroAccuracy: summary.MacroAccuracy,
		GroupCoverage: summary.GroupCoverage, QuestionCoverage: summary.QuestionCoverage,
	}
}

func toAPICohortExecution(execution cohorttask.Execution) *api.KnowledgeEvalCohortExecution {
	return &api.KnowledgeEvalCohortExecution{
		Dataset: execution.Dataset, Partition: execution.Partition, GroupID: execution.GroupID,
		Questions: int32(execution.Questions), Status: execution.Status,
		IneligibleReason: optionalString(execution.IneligibleReason),
		TaskID:           optionalString(execution.TaskID), Error: optionalString(execution.Error),
		RunID:              optionalString(execution.RunID),
		EvaluatedQuestions: int32(execution.EvaluatedQuestions),
		CorrectQuestions:   int32(execution.CorrectQuestions), Accuracy: execution.Accuracy,
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
