@endpoint
Feature: Close bill endpoint behavior

  Background:
    Given I remember "create_idem" as a unique idempotency key
    And I remember "create_account" as a unique idempotency key
    And I remember "create_external_ref" as a unique idempotency key
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
    And I store the response JSON field "bill_id" as "bill_id"

  @EP_CLOSE_001
  Scenario: EP-CLOSE-001 invalid bill id is rejected
    Given I remember "close_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "close_idem"
    And I set the request body to JSON:
      """
      {}
      """
    When I send a "POST" request to "/v1/bills/not-a-uuid/close"
    Then the response status should be 400
    And the response body should contain "bill_id must be a valid UUID"

  @EP_CLOSE_002
  Scenario: EP-CLOSE-002 missing bill returns not found
    Given I remember "close_idem" as a unique idempotency key
    And I remember "missing_bill_id" as UUID "99999999-9999-4999-8999-999999999999"
    And I set header "Idempotency-Key" from variable "close_idem"
    And I set the request body to JSON:
      """
      {}
      """
    When I send a "POST" request to "/v1/bills/{{missing_bill_id}}/close"
    Then the response status should be 404
    And the response body should contain "bill not found"

  @EP_CLOSE_003
  Scenario: EP-CLOSE-003 missing idempotency key is rejected
    Given I clear header "Idempotency-Key"
    And I set the request body to JSON:
      """
      {}
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/close"
    Then the response status should be 400
    And the response body should contain "Idempotency-Key"

  @EP_CLOSE_004
  Scenario: EP-CLOSE-004 exact replay returns the accepted close response
    Given I remember "close_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "close_idem"
    And I set the request body to JSON:
      """
      {}
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/close"
    Then the response status should be 202
    And the response JSON field "bill_status" should equal "OPEN"
    And I store the response JSON field "bill_id" as "closed_bill_id"
    When I send a "POST" request to "/v1/bills/{{bill_id}}/close"
    Then the response status should be 202
    And the response JSON field "bill_id" should equal variable "closed_bill_id"
    And the response JSON field "bill_status" should equal "OPEN"
