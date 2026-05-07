@endpoint
Feature: Get bill endpoint behavior

  @EP_GETBILL_001
  Scenario: EP-GETBILL-001 invalid bill id is rejected
    When I send a "GET" request to "/v1/bills/not-a-uuid"
    Then the response status should be 400
    And the response body should contain "bill_id must be a valid UUID"

  @EP_GETBILL_002
  Scenario: EP-GETBILL-002 missing bill returns not found
    Given I remember "missing_bill_id" as UUID "99999999-9999-4999-8999-999999999999"
    When I send a "GET" request to "/v1/bills/{{missing_bill_id}}"
    Then the response status should be 404
    And the response body should contain "bill not found"
