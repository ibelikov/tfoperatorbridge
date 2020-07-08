package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/hashicorp/terraform/plugin"
	"github.com/hashicorp/terraform/providers"
	"github.com/zclconf/go-cty/cty"
)

func useProviderToTalkToAzure(provider *plugin.GRPCProvider) {
	providerConfigBlock := provider.GetSchema().Provider.Block

	// We need a set of cty.Value which maps to the schema of the provider's configuration block.
	// NOTE:
	// 1. If the schema has optional elements they're NOT optional in the cty.Value. The cty.Value structure must include all fields
	//    specified in the schema. The values of the attributes can be empy if they're optional. To get this we use `EmptyValue` on the schema
	//    this iterates the schema and creates a `cty.ObjectVal` which maps to the schema with each attribute set to empty.
	// 2. If the schema includes a List item with a min 1 length the `EmptyValue` will no create a valid ObjectVal for the schema.
	//    It will create an empty list item `[]stringval{}` as this doesn't have 1 item it doesn't match the schema. What is needed is a list with 1 item.
	//    When these items are missing the error messages are of the format `features attribute is required`
	// 3. When the `cty.ObjectVal` doesn't follow the required schema the error messages provided back don't make this immediately clear.
	//    You may for example receive a message of `attribute 'use_msi' bool is required` when the error was introducing the wrong structure for the `features` list
	configProvider := providerConfigBlock.EmptyValue()

	// Here is an example of a list min 1.
	// The `features` block in the Azure RM provider
	//
	// provider "azurerm" {
	// 	version = "=2.0.0"
	// 	features {}
	// }
	//
	// Represented as YAML this would be:
	//
	// features:
	// - ~

	// Workaround to create a `cty.ListVal` for `features` block with one blank item in it.
	// Get block definition
	featuresType := providerConfigBlock.BlockTypes["features"]
	// Create a map to represent the block
	featuresBlockMap := map[string]cty.Value{}
	log.Println(featuresType)
	// Get each of the nested blocks required in the block and create
	// empty items for them. Insert them into the featuresBlockMap
	for name, nestedBlock := range featuresType.BlockTypes {
		featuresBlockMap[name] = nestedBlock.EmptyValue()
	}
	configValueMap := configProvider.AsValueMap()
	// On the provider config block set the `features` attribute to be a list with an instance of the features block in it.
	configValueMap["features"] = cty.ListVal([]cty.Value{cty.ObjectVal(featuresBlockMap)})

	configFull := cty.ObjectVal(configValueMap)

	// Call the `PrepareProviderConfig` with the config object. This returns a version of that config with the
	// required default setup as `PreparedConfig` under the response object.
	// Warning: Diagnostics houses errors, the typical go err pattern isn't followed - must check `resp.Diagnostics.Err()`
	prepConfigResp := provider.PrepareProviderConfig(providers.PrepareProviderConfigRequest{
		Config: configFull,
	})
	if prepConfigResp.Diagnostics.Err() != nil {
		log.Println(prepConfigResp.Diagnostics.Err().Error())
		panic("Failed to prepare config")
	}

	// Lets set the values we need to set while we have the value map
	configValueMap = prepConfigResp.PreparedConfig.AsValueMap()
	configValueMap["client_id"] = cty.StringVal(os.Getenv("ARM_CLIENT_ID"))
	configValueMap["client_secret"] = cty.StringVal(os.Getenv("ARM_CLIENT_SECRET"))
	configValueMap["tenant_id"] = cty.StringVal(os.Getenv("ARM_TENANT_ID"))
	configValueMap["subscription_id"] = cty.StringVal(os.Getenv("ARM_SUBSCRIPTION_ID"))

	// Now we have a prepared config we can configure the provider.
	// Warning (again): Diagnostics houses errors, the typical go err pattern isn't followed - must check `resp.Diagnostics.Err()`
	configureProviderResp := provider.Configure(providers.ConfigureRequest{
		Config: cty.ObjectVal(configValueMap),
	})
	if configureProviderResp.Diagnostics.Err() != nil {
		log.Println(configureProviderResp.Diagnostics.Err().Error())
		panic("Failed to configure provider")
	}

	// Example 1: Read an subscription azurerm datasource
	// readSubscriptionDataSource(provider)

	// Example 2: Create a resource group
	resourceName := "azurerm_resource_group"
	rgSchema := provider.GetSchema().ResourceTypes[resourceName]
	rgConfigValueMap := rgSchema.Block.EmptyValue().AsValueMap()

	// Config from the CRD
	rgName := "tob" + RandomString(12)
	rgConfigValueMap["display_name"] = cty.StringVal("test1")
	rgConfigValueMap["location"] = cty.StringVal("westeurope")
	rgConfigValueMap["name"] = cty.StringVal(rgName)

	log.Println(fmt.Sprintf("-------------------> Testing with %q", rgName))

	// Create RG
	state1 := planAndApplyConfig(provider, resourceName, cty.ObjectVal(rgConfigValueMap), []byte{})

	// Update RG with tags
	rgConfigValueMap["tags"] = cty.MapVal(map[string]cty.Value{
		"testTag": cty.StringVal("testTagValue"),
	})
	state2 := planAndApplyConfig(provider, resourceName, cty.ObjectVal(rgConfigValueMap), state1)

	// Delete?!? - Not working atm
	rgConfigDelete := rgSchema.Block.EmptyValue().AsValueMap()
	rgConfigDelete["id"] = cty.StringVal("/subscriptions/5774ad8f-d51e-4456-a72e-0447910568d3/resourceGroups/" + rgName)
	state3 := planAndApplyConfig(provider, resourceName, cty.ObjectVal(rgConfigDelete), state2)

	_ = state3

	// Todo: Persist the state response from apply somewhere

}

func planAndApplyConfig(provider *plugin.GRPCProvider, resourceName string, config cty.Value, stateSerialized []byte) []byte {

	// TODO - take in config as ObjectVal rather than calling here

	var state cty.Value
	if len(stateSerialized) == 0 {
		schema := provider.GetSchema().ResourceTypes[resourceName]
		state = schema.Block.EmptyValue()
	} else {
		if err := state.GobDecode(stateSerialized); err != nil {
			log.Println(err)
			panic("Failed to decode state")
		}
	}

	planResponse := provider.PlanResourceChange(providers.PlanResourceChangeRequest{
		TypeName:         resourceName,
		PriorState:       state,  // State after last apply or empty if non-existent
		ProposedNewState: config, // Config from CRD representing desired state
		Config:           config, // Config from CRD representing desired state ? Unsure why duplicated but hey ho.
	})

	if planResponse.Diagnostics.Err() != nil {
		log.Println(planResponse.Diagnostics.Err().Error())
		panic("Failed planning resource")
	}

	applyResponse := provider.ApplyResourceChange(providers.ApplyResourceChangeRequest{
		TypeName:     resourceName,              // Working theory:
		PriorState:   state,                     // This is the state from the .tfstate file before the apply is made
		Config:       config,                    // The current HCL configuration or what would be in your terraform file
		PlannedState: planResponse.PlannedState, // The result of a plan (read / diff) between HCL Config and actual resource state
	})
	if applyResponse.Diagnostics.Err() != nil {
		log.Println(applyResponse.Diagnostics.Err().Error())
		panic("Failed applying resourceGroup")
	}

	resultState, err := applyResponse.NewState.GobEncode()

	if err != nil {
		log.Println(err)
		panic("Failed to encode state")
	}

	return resultState
}

func readSubscriptionDataSource(provider *plugin.GRPCProvider) {
	// Now lets use the provider to read from `azurerm_subscription` data source
	// First lets get the Schema for the datasource.
	subDataSourceSchema := provider.GetSchema().DataSources["azurerm_subscription"]
	// Now lets get an empty value map which represents that schema with empty attributes
	subConfigValueMap := subDataSourceSchema.Block.EmptyValue().AsValueMap()
	// Then lets give the data source a display name as this is the only required field here.
	// NOTE: display name is the section following the resource declaration in HCL
	// data "azurerm_subscription" "display_name" here
	subConfigValueMap["display_name"] = cty.StringVal("testing1")

	// Then package this back up as an objectVal and submit it to the provider
	readResp := provider.ReadDataSource(providers.ReadDataSourceRequest{
		TypeName: "azurerm_subscription",
		Config:   cty.ObjectVal(subConfigValueMap),
	})

	// Check it didn't error.
	if readResp.Diagnostics.Err() != nil {
		log.Println(readResp.Diagnostics.Err().Error())
		panic("Failed reading subscription")
	}

	log.Println("Read subscription data")
	log.Println(readResp.State)

}

func RandomString(n int) string {
	rand.Seed(time.Now().UnixNano())

	var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

	s := make([]rune, n)
	for i := range s {
		s[i] = letters[rand.Intn(len(letters))]
	}
	return string(s)
}
