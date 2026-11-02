# Black Death simulation — Ireland

What if the Black Death broke out in Ireland? Compare **1348** conditions (the year it really arrived, with no medicine) with **2026** conditions (antibiotics, cars, and a public-health response modelled on Ireland's handling of COVID-19). Both use the real 2025 population of the whole island.

```bash
go run ./src
```

The whole island fits in the window, and each dot is **about 35 people**.

## Controls

| Key | What it does |
|---|---|
| **Arrow keys** or drag | move around the map |
| **+ / −** or mouse wheel | zoom in and out (up to about 60 km across, enough to see single dots in Dublin) |
| **0** | show the whole island again |
| **Y** | switch between **2026** and **1348** rules (starts a new outbreak) |
| **B** | switch background: satellite, beige map, population (with a key) |
| **V** | switch view: dots, % infected now, % dead |
| **[** and **]** | slow down / speed up |
| **Click** a county | seal it or open it |
| **Space** | pause |
| **R** | start a new outbreak |

The days stop when nobody is infected or incubating and no plague is left in the rats; the status line then says OUTBREAK OVER, and **R** starts a new outbreak. (Rats can restart an outbreak after the last human case, so it doesn't stop at the first day with no sick people.)

Each outbreak starts at a random place, more likely where more people live. A yellow ring marks it, and the status line names the nearest town.

| Colour | Meaning |
|---|---|
| green | healthy |
| orange / yellow | caught bubonic / pneumonic plague, not ill yet |
| red | ill with bubonic plague |
| purple | ill with pneumonic plague (spreads through the air) |
| blue | recovered and immune |
| brown shading | plague among the rats and fleas |
| thin lines and names | the 32 counties |
| red tint, name on a red label | a sealed county (see below) |

The dead are not drawn, but they are counted. The numbers at the top of the window and under the map are in real people (dots × about 35), so they move in steps of about 35.

### County lockdowns

Counties close their borders with the same rule as the towns in our Team-Dolphin-Dog simulation:

- **A county seals** when 0.5% of the people in it are ill (bubonic or pneumonic). It **reopens** when that falls below 0.25%. The setting is `lockdownAt` in `response.go`.
- **Click a county** to seal or open it yourself. After that the rule leaves it alone until the next outbreak.
- **While a county is sealed**, nobody crosses its border: daily movement and trips that would cross it don't happen. On the day it closes, everyone is put on the right side first. Travellers and people visiting go home, and so do diagnosed people isolating at home. Ambulances taking patients to hospital can still cross.
- **Rats and fleas still cross county borders.** A border closure doesn't stop them.
- **1348** doesn't seal counties by itself, but you can still click them.

The 0.5% is lower than the 5% the towns use, because in 2026 no county ever gets near 5% ill at once (1–2% at most). The status line shows how many counties are sealed, and the CSV has a `counties_sealed` column.

### Isolation and quarantine

In 2026, the quarantine works like the one in our Team-Dolphin-Dog simulation, with numbers for Ireland:

- **Every county has an isolation hospital**, drawn as a white square with a red cross where the most people in the county live. It turns pale red when it is full. Zoom in to see beds used / beds.
- **Beds come from real bed numbers.** The Republic has about 2.1 public acute beds per 1,000 people and Northern Ireland about 3.0. A fifth of them are freed for plague by cancelling planned care, as hospitals did in March 2020. That's about 3,300 beds for the whole island.
- **A diagnosed person goes to their county's hospital.** If it is full, an ambulance takes them to the nearest hospital with a free bed, even across a sealed border. Patients can't infect anyone. When the illness ends, survivors go home.
- **When every bed on the island is taken, diagnosed people isolate at home** with antibiotics. They stay put but still infect a quarter as many people as before, because they share a home. Pneumonic plague needs hospital care, so at home it kills 1.5 times as often (46% instead of 31%).
- **Close contacts are quarantined for 7 days.** For each diagnosed pneumonic case, tracers look for up to 5 close contacts (one dot is about 35 people, so that's about 5 contacts each), starting with the people the case infected. Those reached in time get 7 days of preventive antibiotics, which stops the illness if they caught it, and stay at home for 7 days. A doctor checks them every day, so if they fall ill anyway they are diagnosed that day.

Plague guidance only asks contacts who refuse antibiotics to stay at home. Quarantining every traced contact is the stricter COVID-19-style rule, and an assumption here.

The third status line shows how many people are in hospital, isolating at home and in quarantine, and the CSV has `in_hospital`, `isolating_at_home` and `quarantined` columns (in dots).

### Graphs

Four graphs beside the map follow the outbreak day by day:

- **Everyone**: how many people are in each state, on a scale of the whole island. This is the clearest graph for 1348.
- **Caught it or ill now**: only the four sick states, scaled to the highest so far, so a small 2026 outbreak still shows. The black line is people in hospital, as in Team-Dolphin-Dog.
- **Deaths per day (7-day average)**: averaged over a week, because the counts move in steps of about 35 people.
- **Counties sealed**: out of 32 (2026 only).

The faint upright lines mark the days the response reached each stage: first case confirmed, public health emergency and stay-at-home order.

### Area view

Press **V** to colour the island in 10 km squares instead of dots:

- **% infected now**: the share of each square's people who have caught plague or are ill. This shows where the outbreak is burning right now.
- **% dead**: the share who have died so far, counted where they died. This shows where it has been.

Colours run from pale yellow (0.5%) through orange (2%) and red (10%) to dark red (50% or more), with a key beside the map. Squares with fewer than 5 dots are left blank.

## Settings

Everything is switched on and off at the top of `main.go`:

| Setting | What it does when on |
|---|---|
| `isolateDiagnosed` | diagnosed people go to an isolation hospital, or isolate at home when the beds run out |
| `quarantineContacts` | traced close contacts stay at home for 7 days with a daily check |
| `contactTracing` | close contacts of pneumonic cases are traced and get preventive antibiotics |
| `socialDistancing` | people mix less once emergencies are declared |
| `travelCuts` | trips drop once emergencies are declared |
| `countyLockdowns` | counties close their borders when 0.5% of their people are ill |
| `mapBackground` | beige map available with **B** |
| `populationBackground` | population map available with **B** |
| `saveCSV` | daily counts saved to `plague.csv` when the window closes |
| `batchMode` | `go run ./src -batch 5` runs 5 outbreaks of each era without a window |
| `randomStart` | random starting place (off = always Dublin) |

The six measures only affect 2026.

## The 2026 response

One response covers the whole island, although Northern Ireland has its own health service. The numbers come from how Ireland handled COVID-19 in 2020:

| Setting | Value | Basis |
|---|---|---|
| Public health emergency | 12 days after the first confirmed case | 2020: first case 29 Feb, schools closed 12 Mar |
| Stay-at-home order | 27 days after | 2020: stay-at-home order 27 Mar. Northern Ireland was similar (schools closed 18 Mar, UK lockdown 23 Mar). |
| Put off seeing a doctor by cost | 19% | 25% in the Republic delay GP visits because of cost (about 58% pay €50–80 a visit). GP care is free in Northern Ireland. Weighted by population. |
| Mixing less | 35% at the emergency stage, then 75% | Contacts fell 74% under the 2020 UK lockdown; Ireland's COVID-19 reproduction number fell to 0.7–1.0 by mid-April 2020 |
| Trips still happening | 65%, then 30% | Car traffic fell about 35% after schools closed and about 70% under the stay-at-home order |
| Contacts reached in time | 30% → 40% → 50% (assumption) | The HSE reached 94% of COVID-19 contacts, usually within a day, but only those treated before falling ill count |
| Diagnosis chance | 2% a day at first, rising to 30% (assumption) | Plague is notifiable at once in Ireland, but Irish doctors have not seen it in modern times |
| County lockdowns | sealed while 0.5% or more of a county is ill, reopened below 0.25% | the Team-Dolphin-Dog town rule |
| Plague beds | about 3,300 (a fifth of 16,800 acute beds) | about 11,000 public acute beds in the Republic (3.9 million bed days in 2023, ESRI) and 5,835 in Northern Ireland (NISRA, 2024/25); the fifth is an assumption |
| Isolation | in hospital until the illness ends; at home when beds run out | pneumonic patients need airborne precautions for at least 48 hours of antibiotics (NSW plague guideline) |
| At home | infects a quarter as many; pneumonic deaths × 1.5 (assumptions) | |
| Close contacts | up to 5 per pneumonic case, 7 days of antibiotics and quarantine | 7 days of preventive antibiotics and daily checks (NSW plague guideline) |
| Pest control | within 30 km of each case, after 10 days (assumption) | Rats are the owner's or council's job; the HSE treats homes only in Dublin |
| Death rate when treated | 13% bubonic, 31% pneumonic | Clinical review of treated cases (same as the USA version) |

## Results

From `go run ./src -batch`:

| Rules | Average deaths | Range |
|---|---|---|
| **2026** | **≈ 34,000** (0.5%) | single runs vary a lot (36 runs; standard deviation about 17,000) |
| **1348** | **≈ 4.4 million** (62%) | 4.40 – 4.43 million (5 runs) |

The 2026 row was measured before the current county rule and the isolation hospitals. With both, 24 runs of 2026 averaged about 37,000 deaths (10,500 to 73,000). The plague beds filled up in 15 of the 24 runs, and up to about 2,300 contacts were in quarantine at once.

These are not predictions. Pneumonic plague infects only about 1.3 people per case, so small changes push it either side of growing or dying out.

### What each measure is worth

2026 rules, with one measure switched off at a time. These rows were measured with eight runs each, before county lockdowns were added:

| Switched off | Average deaths |
|---|---|
| nothing (all on) | ≈ 40,000 |
| `isolateDiagnosed` | ≈ 35,000 |
| `contactTracing` | ≈ 35,000 |
| `travelCuts` | ≈ 36,000 |
| `socialDistancing` | **≈ 5.1 million** |

Single runs range from about 15,000 to 77,000, depending mostly on where the outbreak starts. So the first four rows are all the same within that noise.

**County lockdowns**, measured with the earlier rule (seal for 14 days after a county's last diagnosed case, and every county under the stay-at-home order), 36 runs each: about 33,600 deaths with them and 37,600 without. That's about 11% fewer, but it's within the run-to-run noise. By the time counties seal, mixing less and travel cuts have already slowed spread between people. Most 2026 deaths then come from rat fleas, which ignore county borders. Without distancing, 2026 is even worse than 1348 (about 73% die). Faster travel spreads pneumonic plague, which is nearly always fatal untreated, across the island before doctors recognise it.

The USA version shows the same thing: **mixing less decides the outcome**. Ireland's 2026 death toll is far lower than the USA's mainly because its 2020 response was faster and stricter.

## Files

| File | What's in it |
|---|---|
| `src/main.go` | the settings, and the simulation itself |
| `src/response.go` | the 2026 public-health response |
| `src/maps.go` | the map, and the 1348 and 2026 eras |
| `src/live.go` | the window |
| `src/graphs.go` | the four graphs beside the map |
| `src/quarantine.go` | isolation hospitals, isolation at home and quarantine of close contacts |
| `src/maps/` | mask (where dots can go), population, satellite and beige maps, town list, county maps (a county number for every km, and every 250 m for drawing the borders) and county names |

The maps are 380 × 480 pixels, 1 km per pixel, in an equal-area projection centred on Ireland (`+proj=laea +lat_0=53.5 +lon_0=-8`). Great Britain and the Isle of Man are painted as sea. People in coastal pixels whose centre is in the sea were moved onto the nearest land, so the island has 7.08 million people.

## Sources

- Hospital beds: Republic, [ESRI bed capacity report (2025)](https://esri.ie/publications/projections-of-regional-demand-and-bed-capacity-requirements-for-public-acute) and [Irish Times summary](https://www.irishtimes.com/health/2025/05/27/up-to-6800-more-inpatient-beds-will-be-needed-by-2040-says-esri-report); Northern Ireland, [NISRA inpatient statistics 2024/25](https://datavis.nisra.gov.uk/health/ni-inpatient-stats-24-25.html)
- Isolation and close contacts: [NSW Health plague control guideline](https://www.health.nsw.gov.au/Infectious/controlguideline/Pages/plague.aspx)

- COVID-19 timeline: [CSO](https://www.cso.ie/en/releasesandpublications/ep/p-covid19/covid-19informationhub/covid-19inirelandtimeline2020-2023); Northern Ireland: [timeline](https://en.wikipedia.org/wiki/Timeline_of_the_COVID-19_pandemic_in_Northern_Ireland_(2020))
- Cost of GP care: [Ireland: Europe's outlier in primary health care (2025)](https://pmc.ncbi.nlm.nih.gov/articles/PMC11910792/); [Department of Health, Health in Ireland: Key Trends](https://www.gov.ie/en/department-of-health/publications/health-in-ireland-key-trends-primary-care-and-community-service/)
- Traffic in 2020: [CSO transport bulletin](https://www.cso.ie/en/releasesandpublications/br/b-tb/transportbulletin01march2020to27february2021)
- Contact tracing: [HSE parliamentary answer, November 2020](https://hse.ie/eng/about/personalpq/pq/2020-pq-responses/november-2020/pq-33480-20-mick-barry.pdf)
- Contacts under lockdown: [CoMix, BMC Medicine 2020](https://bmcmedicine.biomedcentral.com/track/pdf/10.1186/s12916-020-01597-8); Ireland's reproduction number: [NPHET, 23 April 2020](https://www.gov.ie/en/department-of-health/press-releases/statement-from-the-national-public-health-emergency-team-thursday-23-april)
- Plague notification: [HPSC case definition](https://hpsc.ie/a-z/plague/casedefinitions)
- Rodent control: [HSE pest control](https://www.hse.ie/eng/services/list/1/environ/pest-control.html)
- County boundaries: [Tailte Éireann, Counties – National Statutory Boundaries – 2019 – Generalised 20m](https://data.gov.ie/ga/dataset/counties-national-statutory-boundaries-2019-generalised-20m1) (the Republic's 26); [OSNI Largescale Boundaries – County Boundaries](https://www.opendatani.gov.uk/@land-property-services/osni-open-data-largescale-boundaries-county-boundaries) (Northern Ireland's 6)

## Credits

Population: GHSL GHS-POP 2025, European Commission JRC (CC BY 4.0). Satellite: NASA Blue Marble. Coastlines, lakes and borders: Natural Earth. Counties in the Republic: © Tailte Éireann (CC BY 4.0). Counties in Northern Ireland: OSNI, Land & Property Services; contains public sector information licensed under the Open Government Licence v3.0.
