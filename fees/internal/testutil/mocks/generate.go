package mocks

//go:generate go run github.com/vektra/mockery/v2@v2.53.5 --dir ../../store/billing --name Store --output . --outpkg mocks --filename BillingStore.go --structname BillingStore
//go:generate go run github.com/vektra/mockery/v2@v2.53.5 --dir ../../store/idempotency --name Store --output . --outpkg mocks --filename IdempotencyStore.go --structname IdempotencyStore
//go:generate go run github.com/vektra/mockery/v2@v2.53.5 --dir ../../workflow --name Client --output . --outpkg mocks --filename WorkflowClient.go --structname WorkflowClient
//go:generate go run github.com/vektra/mockery/v2@v2.53.5 --dir ../../workflow --name ActivityStore --output . --outpkg mocks --filename ActivityStore.go --structname ActivityStore
