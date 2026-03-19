package main

func seedCategories() []seedCategory {
	return []seedCategory{
		{Name: "Pain & Fever", Slug: "pain-fever", Description: "OTC pain relief, fever reducers and topical pain support.", SortOrder: 1},
		{Name: "Antibiotics", Slug: "antibiotics", Description: "Prescription antibiotics and anti-infective staples.", SortOrder: 2},
		{Name: "Allergy", Slug: "allergy", Description: "Antihistamines, nasal relief and itch management.", SortOrder: 3},
		{Name: "Cough & Cold", Slug: "cough-cold", Description: "Cold, flu, cough and throat symptom relief.", SortOrder: 4},
		{Name: "Gastro Care", Slug: "gastro-care", Description: "Heartburn, reflux, diarrhea and stomach support.", SortOrder: 5},
		{Name: "Vitamins & Supplements", Slug: "vitamins-supplements", Description: "Immune, daily wellness and deficiency support.", SortOrder: 6},
		{Name: "Dermatology", Slug: "dermatology", Description: "Skin creams, antifungals and wound-care topicals.", SortOrder: 7},
		{Name: "First Aid", Slug: "first-aid", Description: "Bandages, antiseptics and basic first-aid consumables.", SortOrder: 8},
		{Name: "Women's Health", Slug: "womens-health", Description: "Menstrual, fertility, pregnancy and intimate care support.", SortOrder: 9},
		{Name: "Baby Care", Slug: "baby-care", Description: "Infant digestion, sterilising and baby skin care.", SortOrder: 10},
		{Name: "Chronic Care", Slug: "chronic-care", Description: "Common long-term therapy lines requiring pharmacist oversight.", SortOrder: 11},
		{Name: "Eye & Ear", Slug: "eye-ear", Description: "Eye lubricants, eye antibiotics and ear care.", SortOrder: 12},
		{Name: "Oral Care", Slug: "oral-care", Description: "Medicated mouthwash, toothpaste and gum health products.", SortOrder: 13},
	}
}

func seedDeliveryZones() []seedDeliveryZone {
	return []seedDeliveryZone{
		{Name: "CBD & Upper Hill", Slug: "cbd-upper-hill", Fee: 150, EstimatedMins: 45, SortOrder: 1},
		{Name: "Westlands & Parklands", Slug: "westlands-parklands", Fee: 250, EstimatedMins: 60, SortOrder: 2},
		{Name: "Kilimani & Kileleshwa", Slug: "kilimani-kileleshwa", Fee: 250, EstimatedMins: 60, SortOrder: 3},
		{Name: "Lavington & Valley Arcade", Slug: "lavington-valley-arcade", Fee: 300, EstimatedMins: 75, SortOrder: 4},
		{Name: "South B, South C & Langata", Slug: "south-b-south-c-langata", Fee: 320, EstimatedMins: 75, SortOrder: 5},
		{Name: "Eastlands, Kasarani & Roysambu", Slug: "eastlands-kasarani-roysambu", Fee: 380, EstimatedMins: 90, SortOrder: 6},
	}
}

func seedBusinessHours() []seedBusinessHour {
	rows := make([]seedBusinessHour, 0, 7)
	for day := 0; day < 7; day++ {
		rows = append(rows, seedBusinessHour{
			DayOfWeek: day,
			OpenTime:  "08:00",
			CloseTime: "22:00",
			IsOpen:    true,
		})
	}
	return rows
}

func seedProducts() []seedProduct {
	products := append([]seedProduct{}, catalogPart1()...)
	products = append(products, catalogPart2()...)
	products = append(products, catalogPart3()...)
	return products
}
