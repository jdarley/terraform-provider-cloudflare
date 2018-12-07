package cloudflare

import (
	"fmt"
	"log"
	"strings"

	"github.com/cloudflare/cloudflare-go"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/hashicorp/terraform/helper/validation"
	"github.com/pkg/errors"
)

func resourceCloudflareRateLimit() *schema.Resource {
	return &schema.Resource{
		Create: resourceCloudflareRateLimitCreate,
		Read:   resourceCloudflareRateLimitRead,
		Update: resourceCloudflareRateLimitUpdate,
		Delete: resourceCloudflareRateLimitDelete,
		Importer: &schema.ResourceImporter{
			State: resourceCloudflareRateLimitImport,
		},

		SchemaVersion: 0,
		Schema: map[string]*schema.Schema{
			"zone": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			"zone_id": {
				Type:     schema.TypeString,
				Computed: true,
			},

			"threshold": {
				Type:         schema.TypeInt,
				Required:     true,
				ValidateFunc: validation.IntBetween(1, 1000000),
			},

			"period": {
				Type:         schema.TypeInt,
				Required:     true,
				ValidateFunc: validation.IntBetween(1, 86400),
			},

			"action": {
				Type:     schema.TypeList,
				Required: true,
				MinItems: 1,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"mode": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validation.StringInSlice([]string{"simulate", "ban", "challenge", "js_challenge"}, true),
						},

						"timeout": {
							Type:         schema.TypeInt,
							Optional:     true,
							ValidateFunc: validation.IntBetween(1, 86400),
						},

						"response": {
							Type:     schema.TypeList,
							Optional: true,
							MinItems: 1,
							MaxItems: 1,
							Elem: &schema.Resource{
								Schema: map[string]*schema.Schema{
									"content_type": {
										Type:         schema.TypeString,
										Required:     true,
										ValidateFunc: validation.StringInSlice([]string{"text/plain", "text/xml", "application/json"}, true),
									},

									"body": {
										Type:         schema.TypeString,
										Required:     true,
										ValidateFunc: validation.StringLenBetween(0, 10240),
										// maybe good to hash the body before saving in state file?
									},
								},
							},
						},
					},
				},
			},

			"match": {
				Type:     schema.TypeList,
				Optional: true,
				Computed: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"request": {
							Type:     schema.TypeList,
							Optional: true,
							Computed: true,
							MinItems: 1,
							MaxItems: 1,
							Elem: &schema.Resource{
								Schema: map[string]*schema.Schema{
									"methods": {
										Type:     schema.TypeSet,
										Optional: true,
										Computed: true,
										Elem: &schema.Schema{Type: schema.TypeString,
											ValidateFunc: validation.StringInSlice(allowedHTTPMethods, true)},
									},

									"schemes": {
										Type:     schema.TypeSet,
										Optional: true,
										Computed: true,
										Elem: &schema.Schema{Type: schema.TypeString,
											ValidateFunc: validation.StringInSlice(allowedSchemes, true)},
									},

									"url_pattern": {
										Type:         schema.TypeString,
										Optional:     true,
										Computed:     true,
										ValidateFunc: validation.StringLenBetween(0, 1024),
									},
								},
							},
						},

						"response": {
							Type:     schema.TypeList,
							Optional: true,
							Computed: true,
							MinItems: 1,
							MaxItems: 1,
							Elem: &schema.Resource{
								Schema: map[string]*schema.Schema{
									"statuses": {
										Type:     schema.TypeSet,
										Optional: true,
										Computed: true,
										Elem:     &schema.Schema{Type: schema.TypeInt},
									},

									"origin_traffic": {
										Type:     schema.TypeBool,
										Optional: true,
										Computed: true,
									},
								},
							},
						},
					},
				},
			},

			"disabled": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},

			"description": {
				Type:         schema.TypeString,
				Optional:     true,
				ValidateFunc: validation.StringLenBetween(0, 1024),
			},

			"bypass_url_patterns": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},

			"correlate": {
				Type:     schema.TypeList,
				Optional: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"by": {
							Type:         schema.TypeString,
							Optional:     true,
							ValidateFunc: validation.StringInSlice([]string{"nat"}, true),
						},
					},
				},
			},
		},
	}
}

func resourceCloudflareRateLimitCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*cloudflare.API)

	newRateLimit := cloudflare.RateLimit{
		Threshold: d.Get("threshold").(int),
		Period:    d.Get("period").(int),
	}

	newRateLimitMatch, err := expandRateLimitTrafficMatcher(d)
	if err != nil {
		return err
	}
	newRateLimit.Match = newRateLimitMatch

	if disabled, ok := d.GetOk("disabled"); ok {
		newRateLimit.Disabled = disabled.(bool)
	}

	if description, ok := d.GetOk("description"); ok {
		newRateLimit.Description = description.(string)
	}

	if bypassUrlPatterns, ok := d.GetOk("bypass_url_patterns"); ok {
		newRateLimit.Bypass = expandRateLimitBypass(bypassUrlPatterns.(*schema.Set))
	}

	newRateLimit.Correlate, _ = expandRateLimitCorrelate(d)

	newRateLimitAction, err := expandRateLimitAction(d)
	if err != nil {
		return err
	}
	newRateLimit.Action = newRateLimitAction

	zoneName := d.Get("zone").(string)
	zoneId, err := client.ZoneIDByName(zoneName)
	if err != nil {
		return fmt.Errorf("error finding zone %q: %s", zoneName, err)
	}

	log.Printf("[DEBUG] Creating Cloudflare Rate Limit from struct: %+v", newRateLimit)

	r, err := client.CreateRateLimit(zoneId, newRateLimit)
	if err != nil {
		return errors.Wrap(err, "error creating rate limit for zone")
	}

	if r.ID == "" {
		return fmt.Errorf("cailed to find id in Create response; resource was empty")
	}

	d.SetId(r.ID)
	// assume ids are immutable, not going to look it up from the api again
	d.Set("zone_id", zoneId)

	log.Printf("[INFO] Cloudflare Rate Limit ID: %s", d.Id())

	return resourceCloudflareRateLimitRead(d, meta)
}

func resourceCloudflareRateLimitUpdate(d *schema.ResourceData, meta interface{}) error {
	// since api only supports replace, update looks a lot like create...
	client := meta.(*cloudflare.API)
	zoneId := d.Get("zone_id").(string)
	rateLimitId := d.Id()

	updatedRateLimit := cloudflare.RateLimit{
		Threshold: d.Get("threshold").(int),
		Period:    d.Get("period").(int),
	}

	newRateLimitAction, err := expandRateLimitAction(d)
	if err != nil {
		return err
	}
	updatedRateLimit.Action = newRateLimitAction

	newRateLimitMatch, err := expandRateLimitTrafficMatcher(d)
	if err != nil {
		return err
	}
	updatedRateLimit.Match = newRateLimitMatch

	if disabled, ok := d.GetOk("disabled"); ok {
		updatedRateLimit.Disabled = disabled.(bool)
	}

	if description, ok := d.GetOk("description"); ok {
		updatedRateLimit.Description = description.(string)
	}

	if bypassUrlPatterns, ok := d.GetOk("bypass_url_patterns"); ok {
		updatedRateLimit.Bypass = expandRateLimitBypass(bypassUrlPatterns.(*schema.Set))
	}

	updatedRateLimit.Correlate, _ = expandRateLimitCorrelate(d)

	_, err = client.UpdateRateLimit(zoneId, rateLimitId, updatedRateLimit)
	if err != nil {
		return errors.Wrap(err, "error creating rate limit for zone")
	}
	return resourceCloudflareRateLimitRead(d, meta)
}

func expandRateLimitTrafficMatcher(d *schema.ResourceData) (matcher cloudflare.RateLimitTrafficMatcher, err error) {
	v, ok := d.GetOk("match")
	if !ok {
		return
	}
	cfg := v.([]interface{})[0].(map[string]interface{})

	if matchReqIface, ok := cfg["request"]; ok && len(matchReqIface.([]interface{})) > 0 {
		matchReq := matchReqIface.([]interface{})[0].(map[string]interface{})

		requestMatcher := cloudflare.RateLimitRequestMatcher{
			URLPattern: matchReq["url_pattern"].(string),
		}

		if methodsSet, ok := matchReq["methods"]; ok {
			methods := make([]string, methodsSet.(*schema.Set).Len())
			for i, m := range methodsSet.(*schema.Set).List() {
				methods[i] = m.(string)
			}
			requestMatcher.Methods = methods
		}
		if schemesSet, ok := matchReq["schemes"]; ok {
			schemes := make([]string, schemesSet.(*schema.Set).Len())
			for i, s := range schemesSet.(*schema.Set).List() {
				schemes[i] = s.(string)
			}
			requestMatcher.Schemes = schemes
		}
		matcher.Request = requestMatcher
	}
	if matchRespIface, ok := cfg["response"]; ok && len(matchRespIface.([]interface{})) > 0 {
		matchResp := matchRespIface.([]interface{})[0].(map[string]interface{})

		responseMatcher := cloudflare.RateLimitResponseMatcher{}

		if statusesSet, ok := matchResp["statuses"]; ok {
			statuses := make([]int, statusesSet.(*schema.Set).Len())
			for i, s := range statusesSet.(*schema.Set).List() {
				statuses[i] = s.(int)
			}
			responseMatcher.Statuses = statuses
		}

		if originIface, ok := matchResp["origin_traffic"]; ok {
			originTraffic := originIface.(bool)
			responseMatcher.OriginTraffic = &originTraffic
		}
		matcher.Response = responseMatcher
	}
	return
}

func validateRateLimitMode(a map[string]interface{}) error {
	m := a["mode"].(string)
	t, pres := a["timeout"]
	req := !strings.Contains(m, "challenge")

	log.Printf("[INFO] Ratelimit timeout %s specified for mode %s", t, m)

	if req && !pres {
		return fmt.Errorf("timeout required for mode '%s' but not provided", m)
	} else if !req && pres {
		return fmt.Errorf("mode '%s' does not accept a timeout", m)
	}
	return nil
}

func expandRateLimitAction(d *schema.ResourceData) (action cloudflare.RateLimitAction, err error) {
	log.Printf("[INFO] Expanding Rate Limit action")
	// dont need to guard for array length because MinItems is set **and** action is required
	tfAction := d.Get("action").([]interface{})[0].(map[string]interface{})

	errMode := validateRateLimitMode(tfAction)
	if errMode != nil {
		return action, err
	}

	action.Mode = tfAction["mode"].(string)
	action.Timeout = tfAction["timeout"].(int)

	if _, ok := tfAction["response"]; ok && len(tfAction["response"].([]interface{})) > 0 {
		log.Printf("[DEBUG] Cloudflare Rate Limit specified action: %+v \n", tfAction)
		tfActionResponse := tfAction["response"].([]interface{})[0].(map[string]interface{})

		action.Response = &cloudflare.RateLimitActionResponse{
			ContentType: tfActionResponse["content_type"].(string),
			Body:        tfActionResponse["body"].(string),
		}
	}
	return action, nil
}

func expandRateLimitCorrelate(d *schema.ResourceData) (correlate cloudflare.RateLimitCorrelate, err error) {
	v, ok := d.GetOk("correlate")
	if !ok {
		return
	}

	tfCorrelate := v.([]interface{})[0].(map[string]interface{})

	correlate = cloudflare.RateLimitCorrelate{
		By: tfCorrelate["by"].(string),
	}

	return
}

func expandRateLimitBypass(bypassUrlPatterns *schema.Set) []cloudflare.RateLimitKeyValue {
	bypass := make([]cloudflare.RateLimitKeyValue, bypassUrlPatterns.Len())
	for i, urlPattern := range bypassUrlPatterns.List() {
		bypass[i] = cloudflare.RateLimitKeyValue{
			Name:  "url",
			Value: urlPattern.(string),
		}
	}
	return bypass
}

func resourceCloudflareRateLimitRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*cloudflare.API)
	zoneId := d.Get("zone_id").(string)
	rateLimitId := d.Id()

	rateLimit, err := client.RateLimit(zoneId, rateLimitId)
	if err != nil {
		if strings.Contains(err.Error(), "HTTP status 404") {
			log.Printf("[INFO] Resource %s in zone %s no longer exists", rateLimitId, zoneId)
			d.SetId("")
			return nil
		} else {
			return errors.Wrap(err,
				fmt.Sprintf("Error reading rate limit resource from API for resource %s in zone %s", zoneId, rateLimitId))
		}
	}
	log.Printf("[DEBUG] Read Cloudflare Rate Limit from API as struct: %+v", rateLimit)

	d.Set("threshold", rateLimit.Threshold)
	d.Set("period", rateLimit.Period)
	if err := d.Set("match", flattenRateLimitTrafficMatcher(rateLimit.Match)); err != nil {
		log.Printf("[WARN] Error setting match on rate limit %q: %s", d.Id(), err)
	}
	if err := d.Set("action", flattenRateLimitAction(rateLimit.Action)); err != nil {
		log.Printf("[WARN] Error setting action on rate limit %q: %s", d.Id(), err)
	}

	d.Set("correlate", flattenRateLimitCorrelate)
	d.Set("description", rateLimit.Description)
	d.Set("disabled", rateLimit.Disabled)

	bypassUrlPatterns := make([]string, 0)
	for _, bypassItem := range rateLimit.Bypass {
		if bypassItem.Name == "url" {
			bypassUrlPatterns = append(bypassUrlPatterns, bypassItem.Value)
		} else {
			// maybe a new type of bypass was added to api
			log.Printf("[WARN] Unkown bypass type found in rate limit for zone %q: %s", d.Id(), bypassItem.Name)
		}
	}
	if err := d.Set("bypass_url_patterns", bypassUrlPatterns); err != nil {
		log.Printf("[WARN] Error setting bypass_url_patterns on rate limit %q: %s", d.Id(), err)
	}

	return nil
}

func flattenRateLimitTrafficMatcher(cfg cloudflare.RateLimitTrafficMatcher) []map[string]interface{} {
	data := map[string]interface{}{
		"request":  flattenRateLimitRequestMatcher(cfg.Request),
		"response": flattenRateLimitResponseMatcher(cfg.Response),
	}
	return []map[string]interface{}{data}
}

func flattenRateLimitRequestMatcher(cfg cloudflare.RateLimitRequestMatcher) []map[string]interface{} {
	data := map[string]interface{}{
		"methods":     schema.NewSet(schema.HashString, flattenStringList(cfg.Methods)),
		"schemes":     schema.NewSet(schema.HashString, flattenStringList(cfg.Schemes)),
		"url_pattern": cfg.URLPattern,
	}

	return []map[string]interface{}{data}
}

func flattenRateLimitResponseMatcher(cfg cloudflare.RateLimitResponseMatcher) []map[string]interface{} {
	data := map[string]interface{}{}

	if cfg.OriginTraffic != nil {
		data["origin_traffic"] = *cfg.OriginTraffic
	}

	if len(cfg.Statuses) > 0 {
		data["statuses"] = schema.NewSet(IntIdentity, flattenIntList(cfg.Statuses))
	}

	if len(data) > 0 {
		return []map[string]interface{}{data}
	} else {
		return []map[string]interface{}{}
	}
}

func flattenRateLimitAction(cfg cloudflare.RateLimitAction) []map[string]interface{} {
	action := map[string]interface{}{
		"mode":    cfg.Mode,
		"timeout": cfg.Timeout,
	}

	if cfg.Response != nil {
		cfgResponse := *cfg.Response
		actionResponse := map[string]interface{}{
			"content_type": cfgResponse.ContentType,
			"body":         cfgResponse.Body,
		}
		action["response"] = []map[string]interface{}{actionResponse}
	}
	return []map[string]interface{}{action}
}

func flattenRateLimitCorrelate(cfg cloudflare.RateLimitCorrelate) []map[string]interface{} {
	correlate := map[string]interface{}{
		"by": cfg.By,
	}
	return []map[string]interface{}{correlate}
}

func resourceCloudflareRateLimitDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*cloudflare.API)
	zoneId := d.Get("zone_id").(string)
	rateLimitId := d.Id()

	log.Printf("[INFO] Deleting Cloudflare Rate Limit: %s for zone: %s", rateLimitId, zoneId)

	err := client.DeleteRateLimit(zoneId, rateLimitId)
	if err != nil {
		return fmt.Errorf("error deleting Cloudflare Rate Limit for zone: %s", err)
	}

	return nil
}

func resourceCloudflareRateLimitImport(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	client := meta.(*cloudflare.API)

	// split the id so we can lookup
	idAttr := strings.SplitN(d.Id(), "/", 2)
	var zoneName string
	var rateLimitId string
	if len(idAttr) == 2 {
		zoneName = idAttr[0]
		rateLimitId = idAttr[1]
	} else {
		return nil, fmt.Errorf("invalid id (\"%s\") specified, should be in format \"zoneName/rateLimitId\" for import", d.Id())
	}

	zoneId, err := client.ZoneIDByName(zoneName)
	if err != nil {
		return nil, fmt.Errorf("error finding zoneName %q: %s", zoneName, err)
	}

	d.Set("zone", zoneName)
	d.Set("zone_id", zoneId)
	d.SetId(rateLimitId)

	return []*schema.ResourceData{d}, nil
}

// StringInSlice returns a SchemaValidateFunc which tests if the provided value
// is of type string and matches the value of an element in the valid slice
// will test with in lower case if ignoreCase is true
func ValidateAction() schema.SchemaValidateFunc {
	return func(i interface{}, k string) (s []string, es []error) {
		es = append(es, fmt.Errorf("failure validating action"))
		return
	}
}