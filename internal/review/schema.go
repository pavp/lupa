package review

// The JSON Schemas of each run's answer, for agents that enforce one. The answer is still parsed
// and checked by code: a schema only makes a well-formed answer likelier.
const (
	reviewSchema = `{"type":"object","properties":{` +
		`"files_inspected":{"type":"array","items":{"type":"string"}},` +
		`"comments":{"type":"array","items":{"type":"object","properties":{` +
		`"file":{"type":"string"},"severity":{"type":"string","enum":["severe","warning"]},` +
		`"claim":{"type":"string"},"mechanism":{"type":"string"},"fix":{"type":"string"},` +
		`"quote":{"type":"string"},"quote_settles":{"type":"boolean"}},` +
		`"required":["file","severity","claim","mechanism","fix","quote","quote_settles"],"additionalProperties":false}}},` +
		`"required":["files_inspected","comments"],"additionalProperties":false}`

	relocateSchema = `{"type":"object","properties":{"quote":{"type":"string"}},"required":["quote"],"additionalProperties":false}`

	filterSchema = `{"type":"object","properties":{"results":{"type":"array","items":{"type":"object","properties":{` +
		`"id":{"type":"integer"},"outcome":{"type":"string","enum":["corroborated","refuted","inconclusive"]},` +
		`"citation":{"type":"object","properties":{"file":{"type":"string"},"line":{"type":"integer"},"quote":{"type":"string"}},` +
		`"required":["file","line","quote"],"additionalProperties":false}},` +
		`"required":["id","outcome"],"additionalProperties":false}}},"required":["results"],"additionalProperties":false}`

	claimsSchema = `{"type":"object","properties":{"results":{"type":"array","items":{"type":"object","properties":{` +
		`"id":{"type":"integer"},"claim":{"type":"string"},` +
		`"outcome":{"type":"string","enum":["supported","unsupported","not_judgeable"]},` +
		`"file":{"type":"string"},"quote":{"type":"string"}},` +
		`"required":["id","claim","outcome"],"additionalProperties":false}}},"required":["results"],"additionalProperties":false}`

	scoreSchema = `{"type":"object","properties":{"results":{"type":"array","items":{"type":"object","properties":{` +
		`"id":{"type":"integer"},"score":{"type":"integer"},"reason":{"type":"string"}},` +
		`"required":["id","score","reason"],"additionalProperties":false}}},"required":["results"],"additionalProperties":false}`
)
