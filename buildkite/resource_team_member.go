package buildkite

import (
	"context"
	"fmt"
	"log"

	//"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resource_schema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/shurcooL/graphql"
)

type TeamMemberNode struct {
	ID   graphql.String
	Role TeamMemberRole
	UUID graphql.String
	Team TeamNode
	User struct {
		ID graphql.ID
	}
}

type TeamMemberResourceModel struct {
	Id     types.String `tfsdk:"id"`
	Uuid   types.String `tfsdk:"uuid"`
	Role   types.String `tfsdk:"role"`
	TeamId types.String `tfsdk:"team_id"`
	UserId types.String `tfsdk:"user_id"`
}

type TeamMemberResource struct {
	client *Client
}

func NewTeamMemberResource() resource.Resource {
	return &TeamMemberResource{}
}

func (TeamMemberResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team_member"
}

func (tm *TeamMemberResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	tm.client = req.ProviderData.(*Client)
}

func (TeamMemberResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resource_schema.Schema{
		MarkdownDescription: "A team member resourc allows for the management of team team membership for existing organization users.",
		Attributes: map[string]resource_schema.Attribute{
			"id": resource_schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"uuid": resource_schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"team_id": resource_schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The GraphQL ID of the team to add/remove the user to/from.",
			},
			"user_id": resource_schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The GraphQL ID of the user to add/remove.",
			},
			"role": resource_schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The role for the user. Either MEMBER or MAINTAINER.",
				Validators: []validator.String{
					stringvalidator.OneOf("MEMBER", "MAINTAINER"),
				},
			},
		},
	}
}

func (tm *TeamMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, state TeamMemberResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

	if resp.Diagnostics.HasError() {
		return
	}

	log.Printf("Creating team member into team %s ...", plan.TeamId.ValueString())
	apiResponse, err := createTeamMember(
		tm.client.genqlient,
		plan.TeamId.ValueString(),
		plan.UserId.ValueString(),
		TeamMemberRole(*plan.Role.ValueStringPointer()),
	)

	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to create team member",
			fmt.Sprintf("Unable to create team member: %s", err.Error()),
		)
		return
	}

	// Update state with values from API response/plan
	state.Id = types.StringValue(apiResponse.TeamMemberCreate.TeamMemberEdge.Node.Id)
	state.Uuid = types.StringValue(apiResponse.TeamMemberCreate.TeamMemberEdge.Node.Uuid)
	state.TeamId = plan.TeamId
	state.UserId = plan.UserId
	// The role of the user will by default be "MEMBER" if none was entered in the plan
	state.Role = types.StringValue(string(apiResponse.TeamMemberCreate.TeamMemberEdge.Node.Role))

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (tm *TeamMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TeamMemberResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	log.Printf("Reading team member %s ...", state.Id.ValueString())
	apiResponse, err := get(tm.client.genqlient, state.Id.ValueString())

	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to read team member",
			fmt.Sprintf("Unable to read ream member: %s", err.Error()),
		)
		return
	} 

	// Convert fron Node to getNodeTeamMember type 
	if teamMemberNode, ok := apiResponse.GetNode().(*getNodeTeamMember); ok {	
		if teamMemberNode == nil {
			resp.Diagnostics.AddError(
			  "Unable to get team member",
			  "Error getting team member: nil response",
			)
			return
		  }
		updateTeamMemberResourceState(&state, *teamMemberNode)
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	}
}

func (tm *TeamMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
    resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (tm *TeamMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var id, role string

	// Obtain team member's ID from state, new role from plan
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("role"), &role)...)

	if resp.Diagnostics.HasError() {
		return
	}

	log.Printf("Updating team member %s with role %s ...", id, role)
	apiResponse, err := updateTeamMember(tm.client.genqlient, id, TeamMemberRole(role))

	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to update Team member",
			fmt.Sprintf("Unable to update Team member: %s", err.Error()),
		)
		return
	}

	// Update state with revised role
	newRole := types.StringValue(string(apiResponse.TeamMemberUpdate.TeamMember.Role))
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("role"), newRole)...)
}

func (tm *TeamMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var id string

	// Obtain team member's ID from state
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)

	if resp.Diagnostics.HasError() {
		return
	}

	log.Printf("Deleting team member with ID %s ...", id)
	_, err := deleteTeamMember(tm.client.genqlient, id)

	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to delete team member",
			fmt.Sprintf("Unable to delete team member: %s", err.Error()),
		)
		return
	}
}

func updateTeamMemberResourceState(tmr *TeamMemberResourceModel, tmn getNodeTeamMember) {
	tmr.Id = types.StringValue(tmn.Id)
	tmr.Uuid = types.StringValue(tmn.Uuid)
	tmr.TeamId = types.StringValue(tmn.Team.Id)
	tmr.UserId = types.StringValue(tmn.User.Id)
	tmr.Role = types.StringValue(string(tmn.Role))
}