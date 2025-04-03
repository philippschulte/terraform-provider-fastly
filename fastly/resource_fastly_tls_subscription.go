package fastly

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	gofastly "github.com/fastly/go-fastly/v10/fastly"
	"github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/customdiff"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

func resourceFastlyTLSSubscription() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceFastlyTLSSubscriptionCreate,
		ReadContext:   resourceFastlyTLSSubscriptionRead,
		UpdateContext: resourceFastlyTLSSubscriptionUpdate,
		DeleteContext: resourceFastlyTLSSubscriptionDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		// Subscription can only be updated when in "issued" or "pending" state, otherwise needs to delete/recreate
		CustomizeDiff: customdiff.All(
			customdiff.ForceNewIf("configuration_id", resourceFastlyTLSSubscriptionIsStateImmutable),
			customdiff.ForceNewIf("domains", resourceFastlyTLSSubscriptionIsStateImmutable),
			customdiff.ForceNewIf("common_name", resourceFastlyTLSSubscriptionIsStateImmutable),
			customdiff.ValidateValue("domains", resourceFastlyTLSSubscriptionValidateDomains),
			customdiff.ValidateValue("common_name", resourceFastlyTLSSubscriptionValidateCommonName),
			resourceFastlyTLSSubscriptionSetNewComputed,
		),
		Schema: map[string]*schema.Schema{
			"certificate_authority": {
				Type:         schema.TypeString,
				Description:  "The entity that issues and certifies the TLS certificates for your subscription. Valid values are `lets-encrypt`, `globalsign` or `certainly`.",
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice([]string{"lets-encrypt", "globalsign", "certainly"}, false),
			},
			"certificate_id": {
				Type:        schema.TypeString,
				Description: "The certificate ID associated with the subscription.",
				Computed:    true,
			},
			"common_name": {
				Type:        schema.TypeString,
				Description: "The common name associated with the subscription generated by Fastly TLS. If you do not pass a common name on create, we will default to the first TLS domain included. If provided, the domain chosen as the common name must be included in TLS domains.",
				Optional:    true,
				Computed:    true,
			},
			"configuration_id": {
				Type:        schema.TypeString,
				Description: "The ID of the set of TLS configuration options that apply to the enabled domains on this subscription.",
				Optional:    true,
				Computed:    true,
			},
			"created_at": {
				Type:        schema.TypeString,
				Description: "Timestamp (GMT) when the subscription was created.",
				Computed:    true,
			},
			"domains": {
				Type:        schema.TypeSet,
				Description: "List of domains on which to enable TLS.",
				Required:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				MinItems:    1,
			},
			"force_destroy": {
				Type:        schema.TypeBool,
				Description: "Force delete the subscription even if it has active domains. Warning: this can disable production traffic if used incorrectly. Defaults to false.",
				Optional:    true,
				Default:     false,
			},
			"force_update": {
				Type:        schema.TypeBool,
				Description: "Force update the subscription even if it has active domains. Warning: this can disable production traffic if used incorrectly.",
				Optional:    true,
				Default:     false,
			},
			"managed_dns_challenge": {
				Type:        schema.TypeMap,
				Description: "The details required to configure DNS to respond to ACME DNS challenge in order to verify domain ownership.",
				Computed:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Deprecated:  "Use 'managed_dns_challenges' attribute instead",
			},
			"managed_dns_challenges": {
				Type:        schema.TypeSet,
				Description: "A list of options for configuring DNS to respond to ACME DNS challenge in order to verify domain ownership.",
				Computed:    true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"record_name": {
							Type:        schema.TypeString,
							Description: "The name of the DNS record to add. For example `_acme-challenge.example.com`.",
							Computed:    true,
						},
						"record_type": {
							Type:        schema.TypeString,
							Description: "The type of DNS record to add, e.g. `A`, or `CNAME`.",
							Computed:    true,
						},
						"record_value": {
							Type:        schema.TypeString,
							Description: "The value to which the DNS record should point, e.g. `xxxxx.fastly-validations.com`.",
							Computed:    true,
						},
					},
				},
			},
			"managed_http_challenges": {
				Type:        schema.TypeSet,
				Description: "A list of options for configuring DNS to respond to ACME HTTP challenge in order to verify domain ownership. Best accessed through a `for` expression to filter the relevant record.",
				Computed:    true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"record_name": {
							Type:        schema.TypeString,
							Description: "The name of the DNS record to add. For example `example.com`. Best accessed through a `for` expression to filter the relevant record.",
							Computed:    true,
						},
						"record_type": {
							Type:        schema.TypeString,
							Description: "The type of DNS record to add, e.g. `A`, or `CNAME`.",
							Computed:    true,
						},
						"record_values": {
							Type:        schema.TypeSet,
							Description: "A list with the value(s) to which the DNS record should point.",
							Computed:    true,
							Elem:        &schema.Schema{Type: schema.TypeString},
						},
					},
				},
			},
			"state": {
				Type:        schema.TypeString,
				Description: "The current state of the subscription. The list of possible states are: `pending`, `processing`, `issued`, and `renewing`.",
				Computed:    true,
			},
			"updated_at": {
				Type:        schema.TypeString,
				Description: "Timestamp (GMT) when the subscription was updated.",
				Computed:    true,
			},
		},
	}
}

func resourceFastlyTLSSubscriptionCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	conn := meta.(*APIClient).conn

	var configuration *gofastly.TLSConfiguration
	if v, ok := d.GetOk("configuration_id"); ok {
		configuration = &gofastly.TLSConfiguration{ID: v.(string)}
	}

	var domains []*gofastly.TLSDomain
	var domainStrings []string
	for _, domain := range d.Get("domains").(*schema.Set).List() {
		domains = append(domains, &gofastly.TLSDomain{ID: domain.(string)})
		domainStrings = append(domainStrings, domain.(string))
	}

	var commonName *gofastly.TLSDomain
	if v, ok := d.GetOk("common_name"); ok {
		if !contains(domainStrings, v.(string)) {
			return diag.Errorf("domain specified as common_name (%s) must also be in domains (%v)", v, domainStrings)
		}

		commonName = &gofastly.TLSDomain{ID: v.(string)}
	}

	subscription, err := conn.CreateTLSSubscription(&gofastly.CreateTLSSubscriptionInput{
		CertificateAuthority: d.Get("certificate_authority").(string),
		Configuration:        configuration,
		Domains:              domains,
		CommonName:           commonName,
	})
	if err != nil {
		return diag.FromErr(err)
	}

	d.SetId(subscription.ID)

	return resourceFastlyTLSSubscriptionRead(ctx, d, meta)
}

func resourceFastlyTLSSubscriptionRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	log.Printf("[DEBUG] Refreshing TLS Subscription Configuration for (%s)", d.Id())

	conn := meta.(*APIClient).conn

	include := "tls_authorizations"
	subscription, err := conn.GetTLSSubscription(&gofastly.GetTLSSubscriptionInput{
		ID:      d.Id(),
		Include: &include,
	})
	if err, ok := err.(*gofastly.HTTPError); ok && err.IsNotFound() {
		id := d.Id()
		d.SetId("")
		return diag.Diagnostics{
			diag.Diagnostic{
				Severity:      diag.Warning,
				Summary:       fmt.Sprintf("TLS subscription (%s) not found - removing from state", id),
				AttributePath: cty.Path{cty.GetAttrStep{Name: id}},
			},
		}
	} else if err != nil {
		return diag.FromErr(err)
	}

	var domains []string
	for _, domain := range subscription.Domains {
		domains = append(domains, domain.ID)
	}

	// NOTE: there must be only one certificate id included per subscription
	// "pending" and "processing" state may not include the id (for new subscriptions)
	certificateID := ""
	if len(subscription.Certificates) > 0 {
		certificateID = subscription.Certificates[0].ID
	}

	var managedHTTPChallenges []map[string]any
	var managedDNSChallenges []map[string]any
	for _, domain := range subscription.Authorizations {
		for _, challenge := range domain.Challenges {
			if challenge.Type == "managed-dns" {
				if len(challenge.Values) < 1 {
					return diag.Errorf("fastly API returned no record values for Managed DNS Challenges")
				}

				managedDNSChallenges = append(managedDNSChallenges, map[string]any{
					"record_type":  challenge.RecordType,
					"record_name":  challenge.RecordName,
					"record_value": challenge.Values[0],
				})
			} else {
				managedHTTPChallenges = append(managedHTTPChallenges, map[string]any{
					"record_type":   challenge.RecordType,
					"record_name":   challenge.RecordName,
					"record_values": challenge.Values,
				})
			}
		}
	}

	// TODO: This block of code contains a bug where the state file will only include
	// the first domain's challenge data in the case of multi-SAN cert subscriptions.
	// Users should use the new "managed_dns_challenges" attribute instead.
	// We're leaving this for backward compatibility but is planned to be removed in v1.0.0.
	// https://github.com/fastly/terraform-provider-fastly/pull/435
	{
		var managedDNSChallengeOld map[string]string
		for _, challenge := range subscription.Authorizations[0].Challenges {
			if challenge.Type == "managed-dns" {
				if len(challenge.Values) < 1 {
					return diag.Errorf("fastly API returned no record values for Managed DNS Challenge")
				}

				managedDNSChallengeOld = map[string]string{
					"record_type":  challenge.RecordType,
					"record_name":  challenge.RecordName,
					"record_value": challenge.Values[0],
				}
			}
		}

		err = d.Set("managed_dns_challenge", managedDNSChallengeOld)
		if err != nil {
			return diag.FromErr(err)
		}
	}

	err = d.Set("domains", domains)
	if err != nil {
		return diag.FromErr(err)
	}
	err = d.Set("common_name", subscription.CommonName.ID)
	if err != nil {
		return diag.FromErr(err)
	}
	err = d.Set("certificate_id", certificateID)
	if err != nil {
		return diag.FromErr(err)
	}
	err = d.Set("certificate_authority", subscription.CertificateAuthority)
	if err != nil {
		return diag.FromErr(err)
	}

	// NOTE: The configuration_id should change depending on scenario.
	// For example, prior to a subscription renewal the ID is the same.
	// Once a subscription is renewed, we need to search for the latest ID.
	// The following API endpoint is used to search for the latest ID.
	// https://www.fastly.com/documentation/reference/api/tls/custom-certs/domains/#list-tls-domains
	err = d.Set("configuration_id", subscription.Configuration.ID)
	if err != nil {
		return diag.FromErr(err)
	}
	var tlsDomains []*gofastly.TLSDomain
	tlsDomains, _ = conn.ListTLSDomains(&gofastly.ListTLSDomainsInput{
		FilterTLSCertificateID: certificateID,
		Include:                "tls_activations",
		Sort:                   "tls_activations.created_at",
	})
	for _, tlsDomain := range tlsDomains {
		// Activations may be empty (omitempty)
		if tlsDomain.Activations == nil {
			break
		}
		activations := len(tlsDomain.Activations)
		if activations > 0 {
			err = d.Set("configuration_id", tlsDomain.Activations[0].Configuration.ID)
			if err != nil {
				return diag.FromErr(err)
			}
			break
		}
	}

	err = d.Set("created_at", subscription.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return diag.FromErr(err)
	}
	err = d.Set("updated_at", subscription.UpdatedAt.Format(time.RFC3339))
	if err != nil {
		return diag.FromErr(err)
	}
	err = d.Set("state", subscription.State)
	if err != nil {
		return diag.FromErr(err)
	}
	err = d.Set("managed_dns_challenges", managedDNSChallenges)
	if err != nil {
		return diag.FromErr(err)
	}
	err = d.Set("managed_http_challenges", managedHTTPChallenges)
	if err != nil {
		return diag.FromErr(err)
	}

	return nil
}

func resourceFastlyTLSSubscriptionUpdate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	// NOTE: Terraform might trigger an update even when it doesn't make sense.
	//
	// This is because along with the "domains" and "common_name" attributes,
	// there are other attributes a customer might modify, such as
	// "force_update" (which has no effect on the upstream data model).
	//
	// So we don't want to call the API if the customer neither passes a change to
	// domains or to the common_name attributes as that would be a waste of
	// network resources.
	//
	// This is why we wrap the API request in the following conditional check.
	// We then send BOTH "domains" and "common_name" in the API request.
	// This is because they both will have a pre-existing value.
	if d.HasChanges("domains", "common_name") {
		// NOTE: The API doesn't care if the domains are in a different order.
		// I mention this because if it did, then we'd only want to set the Domains
		// field on the input struct if there was a change because we otherwise
		// can't guarantee the order.
		var domains []*gofastly.TLSDomain
		for _, domain := range d.Get("domains").(*schema.Set).List() {
			domains = append(domains, &gofastly.TLSDomain{ID: domain.(string)})
		}

		updates := &gofastly.UpdateTLSSubscriptionInput{
			ID:         d.Id(),
			Force:      d.Get("force_update").(bool),
			CommonName: &gofastly.TLSDomain{ID: d.Get("common_name").(string)},
			Domains:    domains,

			// IMPORTANT: We should always pass the configuration_id to the API.
			Configuration: &gofastly.TLSConfiguration{ID: d.Get("configuration_id").(string)},
		}

		conn := meta.(*APIClient).conn
		_, err := conn.UpdateTLSSubscription(updates)
		if err != nil {
			return diag.FromErr(err)
		}
	}

	// If no meaningful attributes are passed, we just return the read data.
	return resourceFastlyTLSSubscriptionRead(ctx, d, meta)
}

func resourceFastlyTLSSubscriptionDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	conn := meta.(*APIClient).conn

	err := conn.DeleteTLSSubscription(&gofastly.DeleteTLSSubscriptionInput{
		ID:    d.Id(),
		Force: d.Get("force_destroy").(bool),
	})
	return diag.FromErr(err)
}

func resourceFastlyTLSSubscriptionIsStateImmutable(_ context.Context, d *schema.ResourceDiff, _ any) bool {
	state := d.Get("state").(string)
	return state != "issued" && state != "pending"
}

func resourceFastlyTLSSubscriptionSetNewComputed(_ context.Context, d *schema.ResourceDiff, _ any) error {
	// NOTE: This is a workaround for a bug in Terraform core (hashicorp/terraform-plugin-sdk#195)
	// where TypeSet computed attributes are not being updated with the new values upon applying (in an update action).
	// This means that they will not be updated until the second "refresh" or "apply" after the first apply.
	// We should work around this and set the new values immediately upon applying so that other resources
	// that are dependent on this resource can properly see the diff and trigger updates accordingly upon applying.
	if d.HasChange("domains") {
		d.SetNewComputed("managed_dns_challenges")
		d.SetNewComputed("managed_http_challenges")
	}

	return nil
}

// NOTE: Although the RFC spec says it’s case-insensitive, the implementation is varied depending on the software.
// For example, Let's Encrypt doesn't allow uppercase letters. For this reason, Fastly TLS also doesn't support
// uppercase letters in domains. But, Fastly API accepts such inputs and silently converts them to lowercase.
// This would cause state mismatch and diff loop, so we explicitly raise an error to eliminate any confusion.
func resourceFastlyTLSSubscriptionValidateDomains(_ context.Context, v, _ any) error {
	for _, domain := range v.(*schema.Set).List() {
		if domain.(string) != strings.ToLower(domain.(string)) {
			return fmt.Errorf("tls subscription 'domains' must not contain uppercase letters: %s", v.(*schema.Set).List())
		}
	}
	return nil
}

func resourceFastlyTLSSubscriptionValidateCommonName(_ context.Context, v, _ any) error {
	if v.(string) != strings.ToLower(v.(string)) {
		return fmt.Errorf("tls subscription 'common_name' must not contain uppercase letters: %s", v.(string))
	}
	return nil
}
