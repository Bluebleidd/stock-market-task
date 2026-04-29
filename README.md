# Stock Market Task

## Description
This project is a simplified stock market simulation. It allows users to manage wallets and trade stocks with a central Bank. The system tracks stock quantities in both wallets and the Bank, and it records every successful action in an Audit Log.

The application is built to be highly available. It uses an Nginx load balancer to ensure that if one instance of the app is closed (during Chaos Mode), the service stays online and continues to handle requests.

## Technologies
- **Go**
- **PostgreSQL**
- **Nginx**
- **Docker**

## Usage
To run this project, you must have Docker installed on your system. You can start the entire environment using the provided scripts::

1. Open your terminal in the project directory.
2. Run the start script with a port number of your choice:
   - For Linux or macOS: `./start.sh <PORT>`
   - For Windows: `start.bat <PORT>`
   (Example: `./start.sh 8080`)

The application will be available at `localhost:<PORT>`.

## Main Endpoints
- POST /stocks: Set the initial number of stocks in the Bank.
- GET /stocks: See the current state of the Bank.
- POST /wallets/{wallet_id}/stocks/{stock_name}: Buy or sell a single stock.
- GET /wallets/{wallet_id}: Check all stocks owned by a specific wallet.
- GET /log: View the history of all successful operations (sorted by time).
- POST /chaos: Simulate a failure by killing the application instance that handles the request.