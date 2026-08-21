package app

// Screen represents the current view in the application
type Screen int

const (
	ScreenMainMenu Screen = iota
	ScreenPrTypeSelect
	ScreenLoading
	ScreenCommitReview
	ScreenTitleInput
	ScreenConfirmation
	ScreenCreating
	ScreenComplete
	ScreenError
	ScreenBatchRepoSelect
	ScreenBatchConfirmation
	ScreenBatchProcessing
	ScreenBatchSummary
	ScreenViewOpenPrs
	ScreenMergeConfirmation
	ScreenMerging
	ScreenMergeSummary
	ScreenUpdatePrompt
	ScreenUpdating
	ScreenSessionHistory
	ScreenPullBranchSelect
	ScreenPullProgress
	ScreenPullSummary
	ScreenActionsOverview
	ScreenViewAllPrs
	ScreenQaTagSelect
	ScreenSettings
	ScreenFirstRun
	ScreenListEdit
)

func (s Screen) String() string {
	names := []string{
		"MainMenu",
		"PrTypeSelect",
		"Loading",
		"CommitReview",
		"TitleInput",
		"Confirmation",
		"Creating",
		"Complete",
		"Error",
		"BatchRepoSelect",
		"BatchConfirmation",
		"BatchProcessing",
		"BatchSummary",
		"ViewOpenPrs",
		"MergeConfirmation",
		"Merging",
		"MergeSummary",
		"UpdatePrompt",
		"Updating",
		"SessionHistory",
		"PullBranchSelect",
		"PullProgress",
		"PullSummary",
		"ActionsOverview",
		"ViewAllPrs",
		"QaTagSelect",
		"Settings",
		"FirstRun",
		"ListEdit",
	}
	if int(s) < len(names) {
		return names[s]
	}
	return "Unknown"
}

// AppMode represents Single or Batch mode
type AppMode int

const (
	ModeSingle AppMode = iota
	ModeBatch
)
