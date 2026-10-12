package taskruntime

func purposeMutationRefusal(execution Execution) (Response, bool) {
	if err := ValidatePurposeBranch(execution.Purpose, execution.Branch); err != nil {
		result := response(StatusBlocked, execution.Task, "execution purpose or branch is invalid: "+err.Error(), "inspect task runtime purpose evidence", "run-status")
		result.Execution = &execution
		return result, true
	}
	return Response{}, false
}
