package onelake

// The types below port upstream's Models/DataAccessRoleModels.cs. OneLake's
// JSON context writes nulls, so a field is omitempty only where upstream
// marks it JsonIgnore(WhenWritingNull); every other field serializes even
// when nil, matching System.Text.Json's default (write-null) behaviour.

// dataAccessRole ports DataAccessRole.
type dataAccessRole struct {
	Name          string                 `json:"name"`
	ID            *string                `json:"id,omitempty"`
	ETag          *string                `json:"eTag,omitempty"`
	Kind          *string                `json:"kind,omitempty"`
	DecisionRules []decisionRule         `json:"decisionRules"`
	Members       *dataAccessRoleMembers `json:"members"`
}

// decisionRule ports DecisionRule.
type decisionRule struct {
	Effect      *string             `json:"effect"`
	Permission  []decisionRuleScope `json:"permission"`
	Constraints *constraints        `json:"constraints,omitempty"`
}

// constraints ports Constraints (row/column level security).
type constraints struct {
	Columns []columnConstraint `json:"columns,omitempty"`
	Rows    []rowConstraint    `json:"rows,omitempty"`
}

// columnConstraint ports ColumnConstraint.
type columnConstraint struct {
	TablePath    *string  `json:"tablePath"`
	ColumnNames  []string `json:"columnNames"`
	ColumnEffect *string  `json:"columnEffect"`
	ColumnAction []string `json:"columnAction"`
}

// rowConstraint ports RowConstraint.
type rowConstraint struct {
	TablePath *string `json:"tablePath"`
	Value     *string `json:"value"`
}

// decisionRuleScope ports DecisionRuleScope.
type decisionRuleScope struct {
	AttributeName            *string  `json:"attributeName"`
	AttributeValueIncludedIn []string `json:"attributeValueIncludedIn"`
}

// dataAccessRoleMembers ports DataAccessRoleMembers.
type dataAccessRoleMembers struct {
	FabricItemMembers     []fabricItemMember     `json:"fabricItemMembers"`
	MicrosoftEntraMembers []microsoftEntraMember `json:"microsoftEntraMembers"`
}

// fabricItemMember ports FabricItemMember.
type fabricItemMember struct {
	SourcePath *string  `json:"sourcePath"`
	ItemAccess []string `json:"itemAccess"`
}

// microsoftEntraMember ports MicrosoftEntraMember.
type microsoftEntraMember struct {
	ObjectID   *string `json:"objectId"`
	ObjectType *string `json:"objectType,omitempty"`
	TenantID   *string `json:"tenantId"`
}

// dataAccessRoleListResponse ports DataAccessRoleListResponse, the raw
// Fabric API response body for the list endpoint.
type dataAccessRoleListResponse struct {
	Value             []dataAccessRole `json:"value"`
	ContinuationToken *string          `json:"continuationToken,omitempty"`
	ContinuationUri   *string          `json:"continuationUri,omitempty"`
}

// strPtr builds the pointer fields the models above use.
func strPtr(s string) *string { return &s }
