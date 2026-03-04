# Scripts

## fetch_prices_2025.py

Fetches historical PVPC and spot electricity prices for 2025 from the REData API (same source used by the main app) and saves them to an Excel file.

### Prerequisites

```bash
pip install requests openpyxl
```

### Run

```bash
python scripts/fetch_prices_2025.py
```

### Output

Creates `scripts/pvpc_prices_2025.xlsx` with three sheets:

- **Hourly Prices** — every hour of 2025 with PVPC and spot prices (EUR/MWh and ct/kWh)
- **Daily Summary** — min, max, average per day
- **Monthly Summary** — min, max, average per month
