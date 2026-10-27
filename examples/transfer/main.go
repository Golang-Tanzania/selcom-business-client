// Command transfer looks up a recipient, pays them and polls the result.
//
//	SELCOM_BUSINESS_API_KEY=... SELCOM_BUSINESS_KEY_FILE=private_key.pem go run ./examples/transfer
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	business "github.com/Golang-Tanzania/selcom-business-client"
)

func main() {
	if err := run(); err != nil {
		log.Fatalln(err)
	}
}

func run() error {
	keyPEM, err := os.ReadFile(os.Getenv("SELCOM_BUSINESS_KEY_FILE"))
	if err != nil {
		return err
	}

	cln, err := business.New(os.Getenv("SELCOM_BUSINESS_API_KEY"), keyPEM,
		business.WithBaseURL(business.SandboxBaseURL),
		business.WithLogger(func(_ context.Context, msg string, args ...any) { log.Println(msg, args) }),
	)
	if err != nil {
		return err
	}

	// One timeout per gateway call; the poll loop below outlives any single
	// request by minutes.
	call := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 30*time.Second)
	}

	transID := fmt.Sprintf("DEMO-%d", time.Now().UnixNano())

	ctx, cancel := call()
	defer cancel()

	lk, err := cln.Lookup(ctx, business.LookupInput{
		Bank: business.DestinationMPesa, Account: "255712345678", TransactionID: transID, Amount: 10000,
	})
	if err != nil {
		return fmt.Errorf("lookup: %w", err)
	}

	fmt.Printf("recipient: %s, charges: %.0f\n", lk.Data.AccountName, lk.Data.TotalCharges)

	tr, err := cln.Transfer(ctx, business.TransferInput{
		TransactionID:    transID,
		RecipientFiCode:  business.DestinationMPesa,
		RecipientAccount: "255712345678",
		RecipientName:    lk.Data.AccountName,
		Amount:           10000,
		Purpose:          business.PurposePayment,
		Remarks:          "demo",
	})
	if err != nil {
		return fmt.Errorf("transfer: %w", err)
	}

	fmt.Printf("accepted: %s receipt=%s status=%s\n", tr.Result, tr.Data.SelcomReceipt, tr.Data.Status)

	// Never resubmit while the outcome is not final; poll instead.
	for !tr.IsFinal() {
		time.Sleep(3 * time.Minute)

		pollCtx, pollCancel := call()
		st, err := cln.Status(pollCtx, transID)
		pollCancel()
		if err != nil {
			return fmt.Errorf("status: %w", err)
		}

		fmt.Printf("status: %s (%s)\n", st.Data.Status, st.ResultCode)

		if st.IsFinal() {
			break
		}
	}

	return nil
}
