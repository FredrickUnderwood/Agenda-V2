package contract

// RouteOwner scopes destructive route operations to the control-plane owner.
type RouteOwner struct {
	ApplicationID int64  `json:"application_id" binding:"required,gt=0"`
	Env           string `json:"env" binding:"required,oneof=prod stage test"`
}
