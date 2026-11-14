package core_test

import "testing"

func TestRoleAssignments(t *testing.T) {
	base := "/v1/workspaces/" + ws1 + "/roleAssignments"
	ra := `{"id":"` + item1 + `","principal":{"id":"` + op1 + `","type":"Group","displayName":"G","groupDetails":{"groupType":"SecurityGroup"}},"role":"Viewer"}`
	last, h := fake(t, map[string]reply{
		"GET " + base:                  {200, nil, `{"value":[` + ra + `],"continuationToken":"t"}`},
		"GET " + base + "/" + item1:    {200, map[string]string{"Retry-After": "3"}, ra},
		"POST " + base:                 {201, nil, ra},
		"PATCH " + base + "/" + item1:  {200, nil, ra},
		"DELETE " + base + "/" + item1: {400, nil, `{"errorCode":"LastAdmin"}`},
	})
	cs := session(t, staticCred{}, h)

	env, _ := call(t, cs, "core_list-workspace-role-assignments", map[string]any{"workspace-id": ws1})
	expect(t, env, 200, "Success")
	if !sameJSON(env["results"], `{"roleAssignments":[`+ra+`],"continuationToken":"t"}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}
	env, _ = call(t, cs, "core_get-workspace-role-assignment", map[string]any{"workspace-id": ws1, "workspace-role-assignment-id": item1})
	expect(t, env, 200, "Success")
	if !sameJSON(env["results"], `{"roleAssignment":`+ra+`,"retryAfterSeconds":3}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}

	principal := `{"id":"` + op1 + `","type":"Group","groupDetails":{"groupType":"SecurityGroup"}}`
	env, _ = call(t, cs, "core_add-workspace-role-assignment", map[string]any{"workspace-id": ws1, "principal": principal, "role": "Viewer"})
	expect(t, env, 201, "Fabric confirmed role-assignment creation (HTTP 201). No lookup, follow-up read, polling, automatic retry, or update was performed. Do not resubmit this grant.")
	if !sameJSON(last().body, `{"principal":`+principal+`,"role":"Viewer"}`) {
		t.Errorf("body = %s", jsonOf(last().body))
	}
	// A role mismatch in the confirmed response is not success.
	env, _ = call(t, cs, "core_add-workspace-role-assignment", map[string]any{"workspace-id": ws1, "principal": principal, "role": "Admin"})
	expect(t, env, 502, "Fabric confirmed role-assignment creation (HTTP 201), but the returned metadata could not be read, validated against the requested principal and role, or serialized safely. Do not resubmit this grant; inspect workspace assignments separately. No automatic retry was attempted"+help)
	env, _ = call(t, cs, "core_add-workspace-role-assignment", map[string]any{"workspace-id": ws1, "principal": `{"id":"` + op1 + `","type":"User","groupDetails":{}}`, "role": "Viewer"})
	expect(t, env, 400, "Invalid principal JSON. Supply a nonempty id UUID and type, with only the documented writable groupDetails or servicePrincipalProfileDetails for that type. Use valid Unicode; read-only, unknown, and duplicate fields are not accepted.")

	env, _ = call(t, cs, "core_update-workspace-role-assignment", map[string]any{"workspace-id": ws1, "workspace-role-assignment-id": item1, "role": "Viewer"})
	expect(t, env, 200, "Fabric confirmed role-assignment update (HTTP 200). No lookup, follow-up read, creation, polling, or automatic retry was performed. Do not resubmit this update.")

	env, _ = call(t, cs, "core_delete-workspace-role-assignment", map[string]any{"workspace-id": ws1, "workspace-role-assignment-id": item1})
	expect(t, env, 400, "Fabric rejected the role-assignment deletion. Check the workspace and existing assignment UUIDs. The last admin's assignment cannot be deleted. "+
		"Fabric rejected the request; no fallback permission change was attempted. No automatic retry or change to other assignments was attempted. Fabric response HTTP 400. Fabric error code: LastAdmin"+help)
}
