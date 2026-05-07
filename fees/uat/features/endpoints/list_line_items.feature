@endpoint
Feature: List bill line items endpoint behavior

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
    Given I remember "add_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-list-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":10
          },
          {
            "external_reference_id":"line-list-002",
            "occurred_at":"{{billing_end}}",
            "amount_minor":20
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    And the response JSON field "snapshot_total_amount_minor" should equal the number 30

  @EP_LIST_001
  Scenario: EP-LIST-001 invalid bill id is rejected
    When I send a "GET" request to "/v1/bills/not-a-uuid/line-items"
    Then the response status should be 400
    And the response body should contain "bill_id must be a valid UUID"

  @EP_LIST_002
  Scenario: EP-LIST-002 bill not found is rejected
    Given I remember "missing_bill_id" as UUID "99999999-9999-4999-8999-999999999999"
    When I send a "GET" request to "/v1/bills/{{missing_bill_id}}/line-items"
    Then the response status should be 404
    And the response body should contain "bill not found"

  @EP_LIST_003
  Scenario: EP-LIST-003 first page returns next page token
    When I send a "GET" request to "/v1/bills/{{bill_id}}/line-items?page_size=1"
    Then the response status should be 200
    And the response JSON array "items" should have length 1
    And the response JSON field "next_page_token" should not be empty
    And I store the response JSON field "next_page_token" as "next_page_token"

  @EP_LIST_004
  Scenario: EP-LIST-004 second page returns remaining item
    When I send a "GET" request to "/v1/bills/{{bill_id}}/line-items?page_size=1"
    Then the response status should be 200
    And I store the response JSON field "next_page_token" as "next_page_token"
    When I send a "GET" request to "/v1/bills/{{bill_id}}/line-items?page_size=1&page_token={{next_page_token}}"
    Then the response status should be 200
    And the response JSON array "items" should have length 1

  @EP_LIST_005
  Scenario: EP-LIST-005 oversized page size is rejected
    When I send a "GET" request to "/v1/bills/{{bill_id}}/line-items?page_size=101"
    Then the response status should be 400
    And the response body should contain "page_size"

  @EP_LIST_006
  Scenario: EP-LIST-006 invalid page token is rejected
    When I send a "GET" request to "/v1/bills/{{bill_id}}/line-items?page_token=not-base64"
    Then the response status should be 400
    And the response body should contain "page_token"
