package review

// The JSON Schemas of each run's answer, for agents that enforce one. The answer is still parsed
// and checked by code: a schema only makes a well-formed answer likelier.
const (
	reviewSchema = `{"type":"object","properties":{` +
		`"files_inspected":{"type":"array","items":{"type":"string"}},` +
		`"comments":{"type":"array","items":{"type":"object","properties":{` +
		`"file":{"type":"string"},"severity":{"type":"string","enum":["severe","warning"]},` +
		`"claim":{"type":"string"},"mechanism":{"type":"string"},"fix":{"type":"string"},` +
		`"quote":{"type":"string"},"quote_settles":{"type":"boolean"},` + harmProperty + `},` +
		`"required":["file","severity","claim","mechanism","fix","quote","quote_settles","harm"],"additionalProperties":false}}},` +
		`"required":["files_inspected","comments"],"additionalProperties":false}`

	harmProperty = `"harm":{"type":"string","enum":["wrong_data","failure","security","broken_caller","slow","none"]}`

	relocateSchema = `{"type":"object","properties":{"quote":{"type":"string"}},"required":["quote"],"additionalProperties":false}`

	investigateSchema = `{"type":"object","properties":{` +
		`"verdict":{"type":"string","enum":["confirmed","refuted","unproven"]},"reasoning":{"type":"string"},"blocks":{"type":"string"},` +
		`"evidence":{"type":"array","items":{"type":"object","properties":{"file":{"type":"string"},"line":{"type":"integer"},"quote":{"type":"string"}},` +
		`"required":["file","line","quote"],"additionalProperties":false}}},` +
		`"required":["verdict","reasoning","blocks","evidence"],"additionalProperties":false}`

	orchestrateSchema = `{"type":"object","properties":{"groups":{"type":"array","items":{"type":"object","properties":{` +
		`"id":{"type":"integer"},"files_inspected":{"type":"array","items":{"type":"string"}},` +
		`"comments":{"type":"array","items":{"type":"object","properties":{` +
		`"file":{"type":"string"},"severity":{"type":"string","enum":["severe","warning"]},` +
		`"claim":{"type":"string"},"mechanism":{"type":"string"},"fix":{"type":"string"},` +
		`"quote":{"type":"string"},"quote_settles":{"type":"boolean"},` + harmProperty + `,"validation":` + investigateSchema + `},` +
		`"required":["file","severity","claim","mechanism","fix","quote","quote_settles","harm"],"additionalProperties":false}}},` +
		`"required":["id","files_inspected","comments"],"additionalProperties":false}}},"required":["groups"],"additionalProperties":false}`

	claimsSchema = `{"type":"object","properties":{"results":{"type":"array","items":{"type":"object","properties":{` +
		`"id":{"type":"integer"},"claim":{"type":"string"},` +
		`"outcome":{"type":"string","enum":["supported","unsupported","not_judgeable"]},` +
		`"file":{"type":"string"},"quote":{"type":"string"}},` +
		`"required":["id","claim","outcome"],"additionalProperties":false}}},"required":["results"],"additionalProperties":false}`
)
