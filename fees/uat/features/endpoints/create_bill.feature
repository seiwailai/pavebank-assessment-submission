@endpoint
Feature: Create bill endpoint behavior

  @EP_CREATE_001
  Scenario: EP-CREATE-001 create bill successfully
    Given I remember "create_idem" as a unique idempotency key
    And I remember "create_account" as "acct-endpoint-create-001"
    And I remember "create_external_ref" as "bill-endpoint-create-001"
    And I remember "billing_start" as timestamp "now-2h"
    And I remember "billing_end" as timestamp "now+2h"
    And I remember "submission_deadline" as timestamp "now+3h"
    And I set header "Content-Type" to "application/json"
    And I set header "Idempotency-Key" from variable "create_idem"
    And I set the request body to JSON:
      """
      {
        "account_id":"{{create_account}}",
        "external_reference_id":"{{create_external_ref}}",
        "currency_code":"USD",
        "billing_period_start_at":"{{billing_start}}",
        "billing_period_end_at":"{{billing_end}}",
        "line_items_submission_deadline":"{{submission_deadline}}"
      }
      """
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 201
    And the response JSON field "bill_id" should not be empty
    And I store the response JSON field "bill_id" as "created_bill_id"

  @EP_CREATE_002
  Scenario: EP-CREATE-002 missing idempotency key is rejected
    Given I remember "billing_start" as timestamp "now-2h"
    And I remember "billing_end" as timestamp "now+2h"
    And I remember "submission_deadline" as timestamp "now+3h"
    And I set header "Content-Type" to "application/json"
    And I set the request body to JSON:
      """
      {
        "account_id":"acct-endpoint-create-002",
        "external_reference_id":"bill-endpoint-create-002",
        "currency_code":"USD",
        "billing_period_start_at":"{{billing_start}}",
        "billing_period_end_at":"{{billing_end}}",
        "line_items_submission_deadline":"{{submission_deadline}}"
      }
      """
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 400
    And the response body should contain "Idempotency-Key"

  @EP_CREATE_003
  Scenario: EP-CREATE-003 missing account id is rejected
    Given I remember "create_idem" as a unique idempotency key
    And I remember "billing_start" as timestamp "now-2h"
    And I remember "billing_end" as timestamp "now+2h"
    And I remember "submission_deadline" as timestamp "now+3h"
    And I set header "Content-Type" to "application/json"
    And I set header "Idempotency-Key" from variable "create_idem"
    And I set the request body to JSON:
      """
      {
        "account_id":"",
        "external_reference_id":"bill-endpoint-create-003",
        "currency_code":"USD",
        "billing_period_start_at":"{{billing_start}}",
        "billing_period_end_at":"{{billing_end}}",
        "line_items_submission_deadline":"{{submission_deadline}}"
      }
      """
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 400
    And the response body should contain "account_id"

  @EP_CREATE_004
  Scenario: EP-CREATE-004 missing external reference id is rejected
    Given I remember "create_idem" as a unique idempotency key
    And I remember "billing_start" as timestamp "now-2h"
    And I remember "billing_end" as timestamp "now+2h"
    And I remember "submission_deadline" as timestamp "now+3h"
    And I set header "Content-Type" to "application/json"
    And I set header "Idempotency-Key" from variable "create_idem"
    And I set the request body to JSON:
      """
      {
        "account_id":"acct-endpoint-create-004",
        "external_reference_id":"",
        "currency_code":"USD",
        "billing_period_start_at":"{{billing_start}}",
        "billing_period_end_at":"{{billing_end}}",
        "line_items_submission_deadline":"{{submission_deadline}}"
      }
      """
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 400
    And the response body should contain "external_reference_id"

  @EP_CREATE_005
  Scenario: EP-CREATE-005 unsupported currency is rejected
    Given I remember "create_idem" as a unique idempotency key
    And I remember "billing_start" as timestamp "now-2h"
    And I remember "billing_end" as timestamp "now+2h"
    And I remember "submission_deadline" as timestamp "now+3h"
    And I set header "Content-Type" to "application/json"
    And I set header "Idempotency-Key" from variable "create_idem"
    And I set the request body to JSON:
      """
      {
        "account_id":"acct-endpoint-create-005",
        "external_reference_id":"bill-endpoint-create-005",
        "currency_code":"EUR",
        "billing_period_start_at":"{{billing_start}}",
        "billing_period_end_at":"{{billing_end}}",
        "line_items_submission_deadline":"{{submission_deadline}}"
      }
      """
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 400
    And the response body should contain "currency_code"

  @EP_CREATE_006
  Scenario: EP-CREATE-006 billing period end before start is rejected
    Given I remember "create_idem" as a unique idempotency key
    And I remember "billing_start" as timestamp "now+2h"
    And I remember "billing_end" as timestamp "now+1h"
    And I remember "submission_deadline" as timestamp "now+3h"
    And I set header "Content-Type" to "application/json"
    And I set header "Idempotency-Key" from variable "create_idem"
    And I set the request body to JSON:
      """
      {
        "account_id":"acct-endpoint-create-006",
        "external_reference_id":"bill-endpoint-create-006",
        "currency_code":"USD",
        "billing_period_start_at":"{{billing_start}}",
        "billing_period_end_at":"{{billing_end}}",
        "line_items_submission_deadline":"{{submission_deadline}}"
      }
      """
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 400
    And the response body should contain "billing_period_end_at"

  @EP_CREATE_007
  Scenario: EP-CREATE-007 submission deadline before billing end is rejected
    Given I remember "create_idem" as a unique idempotency key
    And I remember "billing_start" as timestamp "now-2h"
    And I remember "billing_end" as timestamp "now+3h"
    And I remember "submission_deadline" as timestamp "now+2h"
    And I set header "Content-Type" to "application/json"
    And I set header "Idempotency-Key" from variable "create_idem"
    And I set the request body to JSON:
      """
      {
        "account_id":"acct-endpoint-create-007",
        "external_reference_id":"bill-endpoint-create-007",
        "currency_code":"USD",
        "billing_period_start_at":"{{billing_start}}",
        "billing_period_end_at":"{{billing_end}}",
        "line_items_submission_deadline":"{{submission_deadline}}"
      }
      """
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 400
    And the response body should contain "line_items_submission_deadline"

  @EP_CREATE_009
  Scenario: EP-CREATE-009 exact idempotent replay returns the same bill
    Given I remember "create_idem" as a unique idempotency key
    And I remember "create_account" as "acct-endpoint-create-009"
    And I remember "create_external_ref" as "bill-endpoint-create-009"
    And I remember "billing_start" as timestamp "now-2h"
    And I remember "billing_end" as timestamp "now+2h"
    And I remember "submission_deadline" as timestamp "now+3h"
    And I set header "Content-Type" to "application/json"
    And I set header "Idempotency-Key" from variable "create_idem"
    And I set the request body to JSON:
      """
      {
        "account_id":"{{create_account}}",
        "external_reference_id":"{{create_external_ref}}",
        "currency_code":"USD",
        "billing_period_start_at":"{{billing_start}}",
        "billing_period_end_at":"{{billing_end}}",
        "line_items_submission_deadline":"{{submission_deadline}}"
      }
      """
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 201
    And I store the response JSON field "bill_id" as "created_bill_id"
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 201
    And the response JSON field "bill_id" should equal variable "created_bill_id"

  @EP_CREATE_010
  Scenario: EP-CREATE-010 same idempotency key with different payload is rejected
    Given I remember "create_idem" as a unique idempotency key
    And I remember "billing_start" as timestamp "now-2h"
    And I remember "billing_end" as timestamp "now+2h"
    And I remember "submission_deadline" as timestamp "now+3h"
    And I set header "Content-Type" to "application/json"
    And I set header "Idempotency-Key" from variable "create_idem"
    And I set the request body to JSON:
      """
      {
        "account_id":"acct-endpoint-create-010",
        "external_reference_id":"bill-endpoint-create-010-a",
        "currency_code":"USD",
        "billing_period_start_at":"{{billing_start}}",
        "billing_period_end_at":"{{billing_end}}",
        "line_items_submission_deadline":"{{submission_deadline}}"
      }
      """
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 201
    Given I set the request body to JSON:
      """
      {
        "account_id":"acct-endpoint-create-010",
        "external_reference_id":"bill-endpoint-create-010-b",
        "currency_code":"USD",
        "billing_period_start_at":"{{billing_start}}",
        "billing_period_end_at":"{{billing_end}}",
        "line_items_submission_deadline":"{{submission_deadline}}"
      }
      """
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 409
    And the response body should contain "different request payload"

  @EP_CREATE_011
  Scenario: EP-CREATE-011 different idempotency key with same business identity is rejected
    Given I remember "create_idem_a" as a unique idempotency key
    And I remember "create_idem_b" as a unique idempotency key
    And I remember "billing_start" as timestamp "now-2h"
    And I remember "billing_end" as timestamp "now+2h"
    And I remember "submission_deadline" as timestamp "now+3h"
    And I set header "Content-Type" to "application/json"
    And I set header "Idempotency-Key" from variable "create_idem_a"
    And I set the request body to JSON:
      """
      {
        "account_id":"acct-endpoint-create-011",
        "external_reference_id":"bill-endpoint-create-011",
        "currency_code":"USD",
        "billing_period_start_at":"{{billing_start}}",
        "billing_period_end_at":"{{billing_end}}",
        "line_items_submission_deadline":"{{submission_deadline}}"
      }
      """
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 201
    Given I set header "Idempotency-Key" from variable "create_idem_b"
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 409
    And the response body should contain "bill already exists"
