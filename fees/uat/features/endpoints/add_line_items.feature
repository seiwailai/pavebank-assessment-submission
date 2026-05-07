@endpoint
Feature: Add line items endpoint behavior

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

  @EP_ADD_001
  Scenario: EP-ADD-001 invalid bill id is rejected
    Given I remember "add_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-invalid-billid-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":100
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/not-a-uuid/line-items"
    Then the response status should be 400
    And the response body should contain "bill_id must be a valid UUID"

  @EP_ADD_002
  Scenario: EP-ADD-002 bill not found is rejected
    Given I remember "add_idem" as a unique idempotency key
    And I remember "missing_bill_id" as UUID "99999999-9999-4999-8999-999999999999"
    And I set header "Idempotency-Key" from variable "add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-not-found-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":100
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{missing_bill_id}}/line-items"
    Then the response status should be 404
    And the response body should contain "bill not found"

  @EP_ADD_003
  Scenario: EP-ADD-003 missing idempotency key is rejected
    Given I clear header "Idempotency-Key"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-missing-idem-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":100
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 400
    And the response body should contain "Idempotency-Key"

  @EP_ADD_004
  Scenario: EP-ADD-004 empty line item list is rejected
    Given I remember "add_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 400
    And the response body should contain "line_items"

  @EP_ADD_005
  Scenario: EP-ADD-005 duplicate external references in one batch are rejected
    Given I remember "add_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-dup-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":100
          },
          {
            "external_reference_id":"line-dup-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":200
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 400
    And the response body should contain "duplicate external_reference_id"

  @EP_ADD_006
  Scenario: EP-ADD-006 non-positive amount is rejected
    Given I remember "add_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-zero-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":0
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 400
    And the response body should contain "amount_minor must be positive"

  @EP_ADD_007
  Scenario: EP-ADD-007 unsupported currency is rejected
    Given I remember "add_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"EUR",
        "line_items":[
          {
            "external_reference_id":"line-eur-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":100
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 400
    And the response body should contain "currency_code"

  @EP_ADD_008
  Scenario: EP-ADD-008 out-of-period line item is rejected
    Given I remember "add_idem" as a unique idempotency key
    And I remember "late_occurred_at" as timestamp "now+8h"
    And I set header "Idempotency-Key" from variable "add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-out-of-period-001",
            "occurred_at":"{{late_occurred_at}}",
            "amount_minor":100
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 400
    And the response body should contain "must fall within the bill billing period"

  @EP_ADD_009
  Scenario: EP-ADD-009 exact idempotent replay returns success without duplication
    Given I remember "add_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-replay-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":125
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    And the response JSON field "success_line_item_ids_map.line-replay-001" should not be empty
    And I store the response JSON field "success_line_item_ids_map.line-replay-001" as "line_replay_id"
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    And the response JSON field "snapshot_total_amount_minor" should equal the number 125
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    And the response JSON field "success_line_item_ids_map.line-replay-001" should equal variable "line_replay_id"
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    And the response JSON field "snapshot_total_amount_minor" should equal the number 125
    When I send a "GET" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    And the response JSON array "items" should have length 1

  @EP_ADD_010
  Scenario: EP-ADD-010 same idempotency key with different payload is rejected
    Given I remember "add_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-idem-mismatch-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":125
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    And the response JSON field "snapshot_total_amount_minor" should equal the number 125
    Given I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-idem-mismatch-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":999
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 409
    And the response body should contain "different request payload"

  @EP_ADD_011
  Scenario: EP-ADD-011 existing line item with different idempotency key returns conflict
    Given I remember "add_idem_a" as a unique idempotency key
    And I remember "add_idem_b" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "add_idem_a"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-conflict-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":125
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    And the response JSON field "snapshot_total_amount_minor" should equal the number 125
    Given I set header "Idempotency-Key" from variable "add_idem_b"
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 409
    And the response body should contain "line items batch rejected"

  @EP_ADD_012
  Scenario: EP-ADD-012 partially valid batch is rejected atomically
    Given I remember "baseline_add_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "baseline_add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-baseline-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":40
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    And the response JSON field "snapshot_total_amount_minor" should equal the number 40
    When I send a "GET" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    And the response JSON array "items" should have length 1
    Given I remember "partial_add_idem" as a unique idempotency key
    And I remember "late_occurred_at" as timestamp "now+8h"
    And I set header "Idempotency-Key" from variable "partial_add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-partial-valid-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":60
          },
          {
            "external_reference_id":"line-partial-invalid-001",
            "occurred_at":"{{late_occurred_at}}",
            "amount_minor":80
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 400
    When I send a "GET" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    And the response JSON array "items" should have length 1
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    And the response JSON field "snapshot_total_amount_minor" should equal the number 40
