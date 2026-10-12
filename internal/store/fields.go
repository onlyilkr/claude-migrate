package store

// KnownFields lists record fields seen in Claude Desktop as of 2026-10.
var KnownFields = map[string]bool{
	"alwaysAllowedReasons": true, "autoChosenInApp": true, "autoModeServerFallbackPrompt": true,
	"backgroundTaskSuggestions": true, "branch": true, "bridgeSessionIds": true,
	"bypassChosenInApp": true, "chromePermissionMode": true, "chromeTabGroupId": true,
	"classifierSummaryEnabled": true, "cliSessionId": true, "completedTurns": true,
	"createdAt": true, "cuAllowedApps": true, "cuGrantFlags": true, "cwd": true,
	"devIntentTriggers": true, "devIntents": true, "effort": true, "effortInherited": true,
	"envScopeId": true, "error": true, "errorAt": true, "forkedFromSessionId": true,
	"gitAnchors": true, "gitAnchorsFolderRealpath": true, "gitAnchorsLookupOnly": true,
	"indexedAt": true, "isArchived": true, "isStarred": true, "lastActivityAt": true,
	"lastAssistantUuid": true, "lastFocusedAt": true, "lastSpawnRootDetected": true,
	"latestUserFrameAt": true, "model": true, "originCwd": true, "pendingSystemReminder": true,
	"permissionMode": true, "postTurnSummary": true, "postTurnSummaryFor": true,
	"previousTitles": true, "priorCliSessionIds": true, "priorErrorMark": true,
	"promptAppendSnapshot": true, "prs": true, "publishedArtifacts": true,
	"remoteControlAutoEligible": true, "remoteControlUserEnabled": true,
	"remoteMcpServersConfig": true, "reportFindingsCard": true,
	"resolvedBackgroundTaskSuggestions": true, "rewindEdges": true, "scheduledTaskId": true,
	"scratchOfferFolder": true, "scratchPromptRecents": true, "sessionId": true,
	"sessionPermissionUpdates": true, "sessionSettings": true, "sourceBranch": true,
	"spawnSeed": true, "spawnedFrom": true, "steeredByRemoteClient": true,
	"subagentsTruncatedFor": true, "terminalClaudeTabOrdinal": true, "title": true,
	"titleSource": true, "titleTurn": true, "toolSurfaceSnapshot": true,
	"transcriptModelStates": true, "worktreeName": true, "worktreePath": true,
	"writtenBranches": true,
}
