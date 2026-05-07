@lifecycle
Feature: Open bill happy path lifecycle

  @LC_OPEN_HAPPY_001
  Scenario: LC-OPEN-HAPPY-001 open bill progresses to closed with the correct values
    Given I remember "create_idem" as a unique idempotency key
    And I remember "billing_start" as timestamp "now-5m"
    And I remember "billing_end" as timestamp "now+65m"
    And I remember "submission_deadline" as timestamp "now+70m"
    And I set header "Content-Type" to "application/json"
    And I set header "Idempotency-Key" from variable "create_idem"
    And I set the request body to JSON:
      """
      {
        "account_id":"acct-lifecycle-open-001",
        "external_reference_id":"bill-lifecycle-open-001",
        "currency_code":"USD",
        "billing_period_start_at":"{{billing_start}}",
        "billing_period_end_at":"{{billing_end}}",
        "line_items_submission_deadline":"{{submission_deadline}}"
      }
      """
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 201
    And I store the response JSON field "bill_id" as "bill_id"
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    And the response JSON field "snapshot_total_amount_minor" should equal the number 0
    Given I remember "valid_add_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "valid_add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-open-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":120
          },
          {
            "external_reference_id":"line-open-002",
            "occurred_at":"{{billing_start}}",
            "amount_minor":80
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    And the response JSON field "success_line_item_ids_map.line-open-001" should not be empty
    And the response JSON field "success_line_item_ids_map.line-open-002" should not be empty
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    And the response JSON field "snapshot_total_amount_minor" should equal the number 200
    Given I remember "partial_batch_idem" as a unique idempotency key
    And I remember "outside_period" as timestamp "now+8h"
    And I set header "Idempotency-Key" from variable "partial_batch_idem"
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
            "occurred_at":"{{outside_period}}",
            "amount_minor":90
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 400
    And the response body should contain "must fall within the bill billing period"
    Given I set header "Idempotency-Key" from variable "valid_add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-open-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":120
          },
          {
            "external_reference_id":"line-open-002",
            "occurred_at":"{{billing_start}}",
            "amount_minor":80
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    Given I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-open-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":999
          },
          {
            "external_reference_id":"line-open-002",
            "occurred_at":"{{billing_start}}",
            "amount_minor":80
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 409
    And the response body should contain "different request payload"
    Given I remember "conflict_add_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "conflict_add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-open-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":120
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 409
    And the response body should contain "line items batch rejected"
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    And the response JSON field "snapshot_total_amount_minor" should equal the number 200
    Given I remember "close_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "close_idem"
    And I set the request body to JSON:
      """
      {}
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/close"
    Then the response status should be 202
    And the response JSON field "bill_status" should equal "OPEN"
    Then within 16 seconds polling "GET" "/v1/bills/{{bill_id}}" every 2 seconds the response JSON field "bill_status" should equal "CLOSED"
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "CLOSED"
    And the response JSON field "final_total_amount_minor" should equal the number 200
    And the response JSON field "snapshot_total_amount_minor" should be absent
    When I send a "GET" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    And the response JSON array "items" should have length 2

  @LC_OPEN_HAPPY_002
  Scenario: LC-OPEN-HAPPY-002 open bill auto-closes after submission deadline without manual close
    Given I remember "create_idem" as a unique idempotency key
    And I remember "billing_start" as timestamp "now-5m"
    And I remember "billing_end" as timestamp "now+11s"
    And I remember "submission_deadline" as timestamp "now+12s"
    And I set header "Content-Type" to "application/json"
    And I set header "Idempotency-Key" from variable "create_idem"
    And I set the request body to JSON:
      """
      {
        "account_id":"acct-lifecycle-open-002",
        "external_reference_id":"bill-lifecycle-open-002",
        "currency_code":"USD",
        "billing_period_start_at":"{{billing_start}}",
        "billing_period_end_at":"{{billing_end}}",
        "line_items_submission_deadline":"{{submission_deadline}}"
      }
      """
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 201
    And I store the response JSON field "bill_id" as "bill_id"
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    Given I remember "valid_add_idem" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "valid_add_idem"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-auto-open-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":125
          },
          {
            "external_reference_id":"line-auto-open-002",
            "occurred_at":"{{billing_start}}",
            "amount_minor":75
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    And the response JSON field "snapshot_total_amount_minor" should equal the number 200
    Then within 30 seconds polling "GET" "/v1/bills/{{bill_id}}" every 1 seconds the response JSON field "bill_status" should equal "CLOSED"
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "CLOSED"
    And the response JSON field "final_total_amount_minor" should equal the number 200
    And the response JSON field "snapshot_total_amount_minor" should be absent
    When I send a "GET" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    And the response JSON array "items" should have length 2

  @LC_OPEN_HAPPY_003
  Scenario: LC-OPEN-HAPPY-003 open bill accepts multiple valid add calls before auto-close
    Given I remember "create_idem" as a unique idempotency key
    And I remember "billing_start" as timestamp "now-5m"
    And I remember "billing_end" as timestamp "now+20s"
    And I remember "submission_deadline" as timestamp "now+24s"
    And I set header "Content-Type" to "application/json"
    And I set header "Idempotency-Key" from variable "create_idem"
    And I set the request body to JSON:
      """
      {
        "account_id":"acct-lifecycle-open-003",
        "external_reference_id":"bill-lifecycle-open-003",
        "currency_code":"USD",
        "billing_period_start_at":"{{billing_start}}",
        "billing_period_end_at":"{{billing_end}}",
        "line_items_submission_deadline":"{{submission_deadline}}"
      }
      """
    When I send a "POST" request to "/v1/bills"
    Then the response status should be 201
    And I store the response JSON field "bill_id" as "bill_id"
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    Given I remember "add_idem_1" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "add_idem_1"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-multi-open-001",
            "occurred_at":"{{billing_start}}",
            "amount_minor":50
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    And the response JSON field "snapshot_total_amount_minor" should equal the number 50
    Given I remember "add_idem_2" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "add_idem_2"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-multi-open-002",
            "occurred_at":"{{billing_start}}",
            "amount_minor":75
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
    Given I remember "add_idem_3" as a unique idempotency key
    And I set header "Idempotency-Key" from variable "add_idem_3"
    And I set the request body to JSON:
      """
      {
        "currency_code":"USD",
        "line_items":[
          {
            "external_reference_id":"line-multi-open-003",
            "occurred_at":"{{billing_start}}",
            "amount_minor":75
          }
        ]
      }
      """
    When I send a "POST" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "OPEN"
    And the response JSON field "snapshot_total_amount_minor" should equal the number 200
    Then within 35 seconds polling "GET" "/v1/bills/{{bill_id}}" every 1 seconds the response JSON field "bill_status" should equal "CLOSED"
    When I send a "GET" request to "/v1/bills/{{bill_id}}"
    Then the response status should be 200
    And the response JSON field "bill_status" should equal "CLOSED"
    And the response JSON field "final_total_amount_minor" should equal the number 200
    And the response JSON field "snapshot_total_amount_minor" should be absent
    When I send a "GET" request to "/v1/bills/{{bill_id}}/line-items"
    Then the response status should be 200
    And the response JSON array "items" should have length 3
